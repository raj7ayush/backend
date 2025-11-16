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

	// Classify the query: is it a creation request or a field question?
	isCreationRequest, err := recommend.ClassifyQuery(ctx, userInput, history, s.model)
	if err != nil {
		// If classification fails, default to creation request to maintain backward compatibility
		isCreationRequest = true
	}

	var response string

	if !isCreationRequest {
		// User is asking about a field - answer without suggesting APIs
		response, err = recommend.AnswerFieldQuestion(ctx, userInput, history, s.model)
		if err != nil {
			return "", trimmedSession, fmt.Errorf("answer field question: %w", err)
		}
	} else {
		// User wants to create something - check if we have all required information
		queryInfo, err := recommend.ExtractQueryInfo(ctx, userInput, history, s.model)
		if err != nil {
			return "", trimmedSession, fmt.Errorf("extract query info: %w", err)
		}

		// Check if all 4 required pieces of information are present
		hasAllInfo := queryInfo.IsAsync != nil &&
			queryInfo.IsUMICompliant != nil &&
			queryInfo.IsPrivate != nil &&
			len(queryInfo.FieldNames) > 0

		if !hasAllInfo {
			// Generate follow-up questions for missing information
			questions, err := recommend.GenerateFollowUpQuestions(ctx, queryInfo, s.model)
			if err != nil {
				return "", trimmedSession, fmt.Errorf("generate follow-up questions: %w", err)
			}
			response = questions
		} else {
			// All information is present - proceed with API recommendation
			prompt := composeConversationAwareRequest(history, userInput)
			api, fields, samplePayload, err := recommend.Recommend1(ctx, s.apis, prompt)
			if err != nil {
				return "", trimmedSession, err
			}
			response = formatRecommendation(api, fields, samplePayload)
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

func formatRecommendation(api apiparser.APIDoc, fields []apiparser.APIField, samplePayload string) string {
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

	return strings.TrimSpace(builder.String())
}
