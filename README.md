# API Recommender Chatbot

This project provides a LangChainGo-based assistant that recommends APIs and drafts sample payloads based on conversation context.

## Prerequisites

- Go 1.21 or newer
- Node.js 18+ and npm (for the React frontend)
- LLM credentials exported via environment variables (see `env.sh`)

## Setup

1. Install Go dependencies:

   ```bash
   cd backend
   go mod tidy
   ```

2. Configure environment variables:

   ```bash
   cd backend
   source env.sh
   ```

   Update `env.sh` with your real token if you have not already.

3. Install frontend dependencies:

   ```bash
   cd ../frontend
   npm install
   ```

## Running in CLI mode

```bash
cd backend
source env.sh
go run . -mode cli -docs api-docs/apis.json
```

You can pass `-session` to resume a prior conversation and `-q` to seed the first user message.

## Running the server + frontend

1. Start the Go server:

   ```bash
   cd backend
   source env.sh
   go run . -mode server -addr :8080 \
     -static ../frontend/dist
   ```

   The server exposes:

   - `POST /api/chat` for chat messages
   - `GET /healthz` for health checks
   - `GET /api/sessions` to list recent conversation sessions (latest first)
   - `GET /api/sessions/{sessionId}/messages` to retrieve the saved history
   - Static assets from the directory supplied via `-static`

2. In another terminal, run the React dev server:

   ```bash
   cd frontend
   npm run dev
   ```

   The dev server proxies `/api` requests to `http://localhost:8080`. Keep both
   processes running for hot-reload while developing the UI.

3. Build the frontend for production:

   ```bash
   npm run build
   ```

   Deploy the generated `frontend/dist/` assets anywhere, or point the Go server to
   that directory using the `-static` flag as shown above.

## Frontend overview

- The React app now features a dual-pane layout: a session navigator on the left
  and a chat workspace on the right. All styling is defined in
  `frontend/src/styles.css` and can be customised there.
- The sidebar pulls its data from `GET /api/sessions`, showing message counts,
  last activity timestamps, and snippets. Selecting a session loads its history
  via `GET /api/sessions/{sessionId}/messages`.
- Use the **New Session** button to start a blank conversation. The composer on
  the right always sends messages to the currently active session. When the model
  responds, the UI refreshes both the message list and the session metadata.
- To switch API base URLs (for example when hosting behind a proxy), set
  `VITE_API_BASE` before running `npm run dev` or `npm run build`.



## Notes

- The conversation history persists in SQLite (`chat_memory.db` by default). Pass `-db` to point to a different file.