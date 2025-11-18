package main

import (
	apiparser "api-recommender/api-parser"
	llmprovider "api-recommender/llm_provider"
	"api-recommender/recommend"
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/memory/sqlite3"
)

const defaultSessionListLimit = 50

type SessionSummary struct {
	ID                 string `json:"id"`
	LastMessageAt      string `json:"lastMessageAt,omitempty"`
	LastMessagePreview string `json:"lastMessagePreview,omitempty"`
	MessageCount       int    `json:"messageCount"`
}

type StoredMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Created string `json:"created,omitempty"`
}

type ChatService struct {
	apis  []apiparser.APIDoc
	db    *sql.DB
	model llms.Model
	table string
}

func NewChatService(apis []apiparser.APIDoc, dbPath string) (*ChatService, error) {
	model, err := llmprovider.NewGroqLLM()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open chat history db: %w", err)
	}

	bootstrapHistory := sqlite3.NewSqliteChatMessageHistory(
		sqlite3.WithDB(db),
		sqlite3.WithDBAddress(dbPath),
		sqlite3.WithSession("bootstrap"),
	)

	return &ChatService{
		apis:  apis,
		db:    db,
		model: model,
		table: bootstrapHistory.TableName,
	}, nil
}

func (s *ChatService) ProcessMessage(ctx context.Context, sessionID, userInput string) (string, string, error) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return "", sessionID, fmt.Errorf("empty user input")
	}

	trimmedSession := strings.TrimSpace(sessionID)
	if trimmedSession == "" {
		trimmedSession = uuid.NewString()
	}

	chatHistory := s.newChatHistory(trimmedSession)

	chatMemory := memory.NewConversationBuffer(
		memory.WithChatHistory(chatHistory),
		memory.WithReturnMessages(true),
		memory.WithInputKey("input"),
		memory.WithOutputKey("output"),
	)

	conversationChain := chains.NewConversation(s.model, chatMemory)

	history := ""
	historyVars, err := conversationChain.Memory.LoadMemoryVariables(ctx, map[string]any{"input": userInput})
	if err != nil {
		return "", sessionID, fmt.Errorf("load history: %w", err)
	}

	if historyVars != nil {
		key := conversationChain.Memory.GetMemoryKey(ctx)
		switch v := historyVars[key].(type) {
		case []llms.ChatMessage:
			history, err = llms.GetBufferString(v, "Human", "AI")
			if err != nil {
				return "", sessionID, fmt.Errorf("format history: %w", err)
			}
		case string:
			history = v
		}
	}

	// Classify the query: is it a creation request or a field question? Is it relevant?
	isCreationRequest, isRelevant, err := recommend.ClassifyQuery(ctx, userInput, history, s.model)
	if err != nil {
		// If classification fails, default to creation request to maintain backward compatibility
		isCreationRequest = true
		isRelevant = true
	}

	var response string

	// Handle irrelevant requests
	if !isRelevant {
		response = "I'm an AI agent for the UMI (Unified Market Interface) project. I can help you with UMI project-related requests like creating assets, bonds, transactions, or answering questions about API fields and project-specific concepts. Your request doesn't seem to be related to the UMI project. How can I help you with UMI-related tasks?"
	} else if !isCreationRequest {
		// User is asking about a field - answer without suggesting APIs
		// Don't use history for field questions - they should be answered based on current question only
		// This prevents lagging behind previous questions
		response, err = recommend.AnswerFieldQuestion(ctx, userInput, "", s.model)
		if err != nil {
			return "", trimmedSession, fmt.Errorf("answer field question: %w", err)
		}
	} else {
		// User wants to create something - detect if this is a new request
		// A new request typically starts with creation keywords
		isNewRequest := isNewCreationRequest(userInput, history)
		
		// For continuation (answering questions), use more history to capture previous Q&A
		// For new requests, use less history
		var recentHistory string
		if isNewRequest {
			// New request - minimal history
			recentHistory = getRecentHistoryForContext(history, 2)
		} else {
			// Continuation - use more history to capture the questions and answers
			recentHistory = getRecentHistoryForContext(history, 10)
		}
		
		// Extract query info - from current request context
		queryInfo, err := recommend.ExtractQueryInfo(ctx, userInput, recentHistory, s.model, isNewRequest)
		if err != nil {
			return "", trimmedSession, fmt.Errorf("extract query info: %w", err)
		}

		// Check if all required pieces of information are present
		hasAllInfo := queryInfo.IsAsync != nil &&
			queryInfo.IsUMICompliant != nil &&
			queryInfo.IsPrivate != nil &&
			len(queryInfo.FieldNames) > 0
		
		// If usecase is mentioned, operation must be specified
		if queryInfo.UseCase != "" && queryInfo.Operation == "" {
			hasAllInfo = false
		}
		
		// If async is true, also need event fields
		if queryInfo.IsAsync != nil && *queryInfo.IsAsync {
			hasAllInfo = hasAllInfo && len(queryInfo.EventFields) > 0
		}

		if !hasAllInfo {
			// Generate follow-up questions for missing information
			questions, err := recommend.GenerateFollowUpQuestions(ctx, queryInfo, s.model)
			if err != nil {
				return "", trimmedSession, fmt.Errorf("generate follow-up questions: %w", err)
			}
			response = questions
		} else {
			// All information is present - proceed with API recommendation
			// Use recent history for context
			prompt := composeConversationAwareRequest(recentHistory, userInput)
			api, fields, samplePayload, eventPayload, err := recommend.Recommend1(ctx, s.apis, prompt, queryInfo)
			if err != nil {
				return "", trimmedSession, err
			}
			response = formatRecommendation(api, fields, samplePayload, eventPayload)
		}
	}

	if err := conversationChain.Memory.SaveContext(ctx,
		map[string]any{"input": userInput},
		map[string]any{"output": response},
	); err != nil {
		return "", trimmedSession, fmt.Errorf("save conversation: %w", err)
	}

	return response, trimmedSession, nil
}

func (s *ChatService) ListSessions(ctx context.Context, limit int) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = defaultSessionListLimit
	}

	query := fmt.Sprintf(`
		SELECT
			session,
			MAX(created) AS last_created,
			(
				SELECT content
				FROM %s m2
				WHERE m2.session = m1.session
				ORDER BY created DESC
				LIMIT 1
			) AS last_content,
			COUNT(*) AS total
		FROM %s m1
		WHERE session IS NOT NULL AND session != ''
		GROUP BY session
		ORDER BY last_created DESC
		LIMIT ?;`, s.table, s.table)

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionSummary
	for rows.Next() {
		var id string
		var lastCreated sql.NullString
		var lastContent sql.NullString
		var total int
		if err := rows.Scan(&id, &lastCreated, &lastContent, &total); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}

		summary := SessionSummary{ID: id, MessageCount: total}
		if lastCreated.Valid {
			summary.LastMessageAt = lastCreated.String
		}
		if lastContent.Valid {
			summary.LastMessagePreview = strings.TrimSpace(lastContent.String)
		}
		sessions = append(sessions, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return sessions, nil
}

func (s *ChatService) GetSessionMessages(ctx context.Context, sessionID string, limit int) ([]StoredMessage, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}

	if limit <= 0 {
		limit = sqlite3.DefaultLimit
	}

	query := fmt.Sprintf("SELECT content, type, created FROM %s WHERE session = ? ORDER BY created ASC LIMIT ?;", s.table)
	rows, err := s.db.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("load session messages: %w", err)
	}
	defer rows.Close()

	var messages []StoredMessage
	for rows.Next() {
		var content string
		var msgType string
		var created sql.NullString
		if err := rows.Scan(&content, &msgType, &created); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		msg := StoredMessage{
			Role:    roleFromMessageType(msgType),
			Content: content,
		}
		if created.Valid {
			msg.Created = created.String
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return messages, nil
}

func (s *ChatService) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *ChatService) newChatHistory(sessionID string) *sqlite3.SqliteChatMessageHistory {
	return sqlite3.NewSqliteChatMessageHistory(
		sqlite3.WithDB(s.db),
		sqlite3.WithSession(sessionID),
		sqlite3.WithTableName(s.table),
	)
}

func roleFromMessageType(value string) string {
	switch value {
	case string(llms.ChatMessageTypeHuman):
		return "user"
	case string(llms.ChatMessageTypeAI):
		return "assistant"
	case string(llms.ChatMessageTypeSystem):
		return "system"
	default:
		return "assistant"
	}
}

func composeConversationAwareRequest(history, latest string) string {
	latest = strings.TrimSpace(latest)
	if history == "" {
		return latest
	}
	return fmt.Sprintf("Conversation so far:\n%s\n\nLatest user request: %s", history, latest)
}

// getRecentHistoryForContext extracts only the last N messages from history for context
func getRecentHistoryForContext(history string, n int) string {
	if history == "" {
		return ""
	}
	
	// Split by message pairs (Human/AI)
	parts := strings.Split(history, "\n\n")
	if len(parts) <= n {
		return history
	}
	
	// Get last N parts
	start := len(parts) - n
	if start < 0 {
		start = 0
	}
	
	return strings.Join(parts[start:], "\n\n")
}

// isNewCreationRequest detects if this is a new creation request (not a continuation)
func isNewCreationRequest(userInput, history string) bool {
	lower := strings.ToLower(userInput)
	
	// Check for creation keywords that indicate a new request
	creationKeywords := []string{"create", "make", "generate", "build", "new", "want to", "need to", "burn", "lock"}
	for _, keyword := range creationKeywords {
		if strings.Contains(lower, keyword) {
			// Check if it's not just answering a question
			// If it contains creation keywords and is not just "yes"/"no", it's a new request
			isJustAnswer := strings.Contains(lower, "yes") || strings.Contains(lower, "no")
			// Also check if it's a full sentence with creation intent
			hasCreationIntent := strings.Contains(lower, keyword) && 
				(strings.Contains(lower, "asset") || strings.Contains(lower, "bond") || 
				 strings.Contains(lower, "transaction") || strings.Contains(lower, "gold") ||
				 strings.Contains(lower, "token"))
			
			if hasCreationIntent || (!isJustAnswer && len(strings.Fields(lower)) > 2) {
				return true
			}
		}
	}
	
	// If it's a short answer (yes/no/field names), it's likely a continuation
	if len(strings.Fields(lower)) <= 3 {
		return false
	}
	
	return false
}

func formatRecommendation(api apiparser.APIDoc, fields []apiparser.APIField, samplePayload, eventPayload string) string {
	var builder strings.Builder
	builder.WriteString("Recommended API:\n")
	builder.WriteString(fmt.Sprintf(" Name: %s\n Path: %s\n Method: %s\n Description: %s\n", api.Name, api.Path, api.Method, api.Description))

	if len(fields) == 0 {
		builder.WriteString("Suggested fields: not required\n")
	} else {
		builder.WriteString("Suggested fields:\n")
		for _, f := range fields {
			builder.WriteString(fmt.Sprintf(" - %s (%s): %s\n", f.Name, f.Type, f.Description))
		}
	}

	samplePayload = strings.TrimSpace(samplePayload)
	
	if samplePayload != "" {
		builder.WriteString("Sample payload:\n")
		builder.WriteString(samplePayload)
		if !strings.HasSuffix(samplePayload, "\n") {
			builder.WriteString("\n")
		}
	}
	
	eventPayload = strings.TrimSpace(eventPayload)
	if eventPayload != "" {
		builder.WriteString("\nEvent payload (for async requests):\n")
		builder.WriteString(eventPayload)
		if !strings.HasSuffix(eventPayload, "\n") {
			builder.WriteString("\n")
		}
	}

	return strings.TrimSpace(builder.String())
}
