package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	apiparser "api-recommender/api-parser"
)

func main() {
	var docPath string
	var initialQuery string
	var dbPath string
	var sessionID string
	var mode string
	var addr string
	var staticDir string
	flag.StringVar(&docPath, "docs", "api-docs/apis.md", "Path to API docs")
	flag.StringVar(&initialQuery, "q", "", "Initial user request/prompt")
	flag.StringVar(&dbPath, "db", "chat_memory.db", "Path to SQLite database for chat history")
	flag.StringVar(&sessionID, "session", "", "Conversation session ID (optional, auto-generated if empty)")
	flag.StringVar(&mode, "mode", "cli", "Mode to run: cli or server")
	flag.StringVar(&addr, "addr", ":8080", "Server listen address (only for server mode)")
	flag.StringVar(&staticDir, "static", "frontend/dist", "Directory containing frontend static assets")
	flag.Parse()

	apis, err := apiparser.ParseAPIDocs(docPath)
	if err != nil {
		log.Fatalf("Failed to parse API docs: %v", err)
	}

	service, err := NewChatService(apis, dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize chat service: %v", err)
	}

	ctx := context.Background()
	defer func() {
		if err := service.Close(); err != nil {
			log.Printf("error closing chat service: %v", err)
		}
	}()

	switch strings.ToLower(mode) {
	case "server":
		runServer(ctx, service, addr, staticDir)
	default:
		runCLI(ctx, service, sessionID, initialQuery)
	}
}

func runCLI(ctx context.Context, service *ChatService, sessionID, initialQuery string) {
	fmt.Println("API Recommender Chatbot (type 'quit' or 'exit' to finish)")
	fmt.Println("---------------------------------------------------------")

	if trimmed := strings.TrimSpace(initialQuery); trimmed != "" {
		response, sid, err := service.ProcessMessage(ctx, sessionID, trimmed)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
		} else {
			sessionID = sid
			fmt.Printf("Session ID: %s\n\n", sessionID)
			fmt.Printf("Assistant:\n%s\n\n", response)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("Ayush: ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Fatalf("Input error: %v", err)
			}
			fmt.Println("\nSee You Later!")
			return
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "quit") || strings.EqualFold(input, "exit") {
			fmt.Println("See You Later!")
			return
		}

		response, sid, err := service.ProcessMessage(ctx, sessionID, input)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			continue
		}
		sessionID = sid
		fmt.Printf("\nAssistant:\n%s\n\n", response)
	}
}

func runServer(ctx context.Context, service *ChatService, addr, staticDir string) {
	log.Printf("Starting API recommender server on %s", addr)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			writeCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method != http.MethodPost {
			writeCORSHeaders(w)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeCORSHeaders(w)

		var req struct {
			SessionID string `json:"sessionId"`
			Message   string `json:"message"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}

		response, sessionID, err := service.ProcessMessage(r.Context(), req.SessionID, req.Message)
		if err != nil {
			http.Error(w, fmt.Sprintf("chat error: %v", err), http.StatusInternalServerError)
			return
		}

		writeJSON(w, map[string]any{
			"sessionId": sessionID,
			"message":   response,
		})
	})

	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			writeCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method != http.MethodGet {
			writeCORSHeaders(w)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeCORSHeaders(w)

		limit := parseLimit(r.URL.Query().Get("limit"))
		sessions, err := service.ListSessions(r.Context(), limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("list sessions error: %v", err), http.StatusInternalServerError)
			return
		}

		writeJSON(w, map[string]any{"sessions": sessions})
	})

	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			writeCORSHeaders(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method != http.MethodGet {
			writeCORSHeaders(w)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeCORSHeaders(w)

		path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
		if path == "" {
			http.Error(w, "session id required", http.StatusBadRequest)
			return
		}

		parts := strings.Split(path, "/")
		sessionID := parts[0]

		if len(parts) == 1 {
			http.Error(w, "resource not found", http.StatusNotFound)
			return
		}

		if parts[1] != "messages" {
			http.Error(w, "resource not found", http.StatusNotFound)
			return
		}

		limit := parseLimit(r.URL.Query().Get("limit"))
		messages, err := service.GetSessionMessages(r.Context(), sessionID, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("load session messages error: %v", err), http.StatusInternalServerError)
			return
		}

		writeJSON(w, map[string]any{
			"sessionId": sessionID,
			"messages":  messages,
		})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeCORSHeaders(w)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	if fi, err := os.Stat(staticDir); err == nil && fi.IsDir() {
		fileServer := http.FileServer(http.Dir(staticDir))
		mux.Handle("/", fileServer)
		log.Printf("Serving static files from %s", staticDir)
	} else {
		log.Printf("Static directory %s not found or not a directory; skipping static file serving", staticDir)
	}

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func parseLimit(raw string) int {
	if raw == "" {
		return 0
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0
	}

	return limit
}

func writeCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
