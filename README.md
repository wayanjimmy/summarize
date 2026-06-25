# summarize

A simple Go REST API for summarizing YouTube videos and text using `pi` and `agy` CLI backends.

## Features

- Async summarization via REST API
- YouTube video transcript extraction (via `yt-dlp` captions)
- Raw text summarization
- Event-driven architecture (embedded NATS)
- Durable workflow execution (go-workflows + SQLite)
- Two engine backends: `pi` and `agy`

## Quick Start

```bash
# Install dependencies
go mod tidy

# Run the server
go run ./cmd/server

# Or build binary
go build -o summarize ./cmd/server
./summarize
```

Server starts on `http://localhost:8080` by default.

## Prerequisites

- Go 1.26+
- `yt-dlp` (for YouTube transcripts)
- `pi` or `agy` CLI (for summarization)

Check dependencies:
```bash
which yt-dlp && yt-dlp --version
which pi && (pi --version || true)
which agy && (agy --version || true)
```

## Configuration

Copy `.env.example` to `.env` and customize:

```bash
cp .env.example .env
```

Key environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DATA_DIR` | `./data` | Database and workdir location |
| `SUMMARIZE_ENGINE` | `pi` | Default engine (`pi` or `agy`) |
| `SUMMARIZE_PROMPT` | (built-in) | Default summarization instruction |
| `SUMMARIZE_PI_BIN` | `pi` | Path to pi binary |
| `SUMMARIZE_AGY_BIN` | `agy` | Path to agy binary |
| `SUMMARIZE_PI_MODEL` | empty | Default model passed to `pi --model` |
| `SUMMARIZE_AGY_MODEL` | empty | Default model passed to `agy --model` |
| `SUMMARIZE_YTDLP_BIN` | `yt-dlp` | Path to yt-dlp binary |
| `SUMMARIZE_RUN_TIMEOUT` | `15m` | Max workflow runtime |
| `SUMMARIZE_MAX_INPUT_CHARS` | `100000` | Max input size before truncation |

## API Reference

### Create Summary (POST /v1/summaries)

Summarize a YouTube video:
```bash
curl -X POST http://localhost:8080/v1/summaries \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "engine": "pi",
    "model": "cpa/tr-qwen3.7-max",
    "prompt": "Summarize with bullet points and action items"
  }'
```

Available `format` values:

| Format | Description | Requires YouTube |
|--------|-------------|------------------|
| `summary` (default) | A clear, structured summary | No |
| `chapters` | Timestamped chapter markers | Yes |
| `thread` | Twitter/X thread (280 chars/post) | No |
| `blog` | Blog post with headings and takeaways | No |

When using a non-`summary` format, the built-in format template takes over the prompt, and any user-provided `prompt` is appended as a refinement. For the `summary` format, existing behavior is preserved.

Summarize raw text:
```bash
curl -X POST http://localhost:8080/v1/summaries \
  -H 'Content-Type: application/json' \
  -d '{
    "text": "Long article text to summarize...",
    "engine": "agy",
    "model": "Gemini 3.5 Flash (Low)"
  }'
```

`model` is optional. If omitted, the server uses `SUMMARIZE_PI_MODEL` or `SUMMARIZE_AGY_MODEL` for the selected engine; if that is also empty, the CLI default is used. Non-empty requested or configured models are validated against the runtime CLI model catalog. Unknown requested models return `400 invalid_request`; an unavailable catalog or missing configured default returns `503 service_unavailable`.

Response (202 Accepted):
```json
{
  "run_id": "8a3b0f2f-1b2c-4c9c-93e1-2e5d1bfb3f2a",
  "status": "queued",
  "status_url": "/v1/runs/8a3b0f2f-1b2c-4c9c-93e1-2e5d1bfb3f2a/status",
  "result_url": "/v1/summaries/8a3b0f2f-1b2c-4c9c-93e1-2e5d1bfb3f2a"
}
```

### List Models (GET /v1/models)

```bash
curl http://localhost:8080/v1/models
```

Response:
```json
{
  "engines": {
    "pi": {
      "models": ["cpa/tr-qwen3.7-max"],
      "default_model": "cpa/tr-qwen3.7-max",
      "status": "available",
      "available": true,
      "stale": false,
      "fetched_at": "2026-06-07T10:00:00Z"
    },
    "agy": {
      "models": ["Gemini 3.5 Flash (Low)"],
      "status": "available",
      "available": true,
      "stale": false,
      "fetched_at": "2026-06-07T10:00:00Z"
    }
  }
}
```

The server lazily queries `pi --list-models` and `agy models`, caches successful results for five minutes, and serves stale cached results if a later refresh fails.

### Get Status (GET /v1/runs/{run_id}/status)

```bash
curl http://localhost:8080/v1/runs/RUN_ID/status
```

Response:
```json
{
  "run_id": "8a3b0f2f-1b2c-4c9c-93e1-2e5d1bfb3f2a",
  "status": "running",
  "stage": "summarizing",
  "created_at": "2026-06-06T10:00:00Z",
  "updated_at": "2026-06-06T10:00:10Z"
}
```

### Get Summary (GET /v1/summaries/{run_id})

```bash
curl http://localhost:8080/v1/summaries/RUN_ID
```

Response (succeeded):
```json
{
  "run_id": "8a3b0f2f-1b2c-4c9c-93e1-2e5d1bfb3f2a",
  "status": "succeeded",
  "stage": "done",
  "input_type": "youtube",
  "source_url": "https://www.youtube.com/watch?v=VIDEO_ID",
  "youtube": {
    "video_id": "VIDEO_ID",
    "title": "Example Video"
  },
  "engine": "pi",
  "prompt": "Summarize with bullet points",
  "format": "summary",
  "truncated": false,
  "summary": "Here is the summary...",
  "transcript_chars": 52344,
  "summary_chars": 4210,
  "created_at": "2026-06-06T10:00:00Z",
  "started_at": "2026-06-06T10:00:01Z",
  "finished_at": "2026-06-06T10:01:45Z"
}
```

### Health Check (GET /healthz)

```bash
curl http://localhost:8080/healthz
```

Response:
```json
{
  "ok": true
}
```

### Diagnostics Dashboard (GET /diag/)

The go-workflows diagnostics dashboard is available on the main server port:

```bash
curl -i http://localhost:8080/diag
open http://localhost:8080/diag/
```

`/diag` redirects to `/diag/` so the dashboard can load its embedded assets. The dashboard is unauthenticated; expose it only on trusted/local deployments or protect it at your proxy.

## Architecture

```
HTTP Client
  │
  │ POST /v1/summaries
  ▼
Go REST API (chi)
  │
  │ validate, create run, publish event
  ▼
Embedded NATS (summaries.requested)
  │
  │ subscriber receives event
  ▼
Workflow Starter
  │
  │ start go-workflow
  ▼
go-workflows Worker
  │
  ├─▶ FetchTranscriptActivity (yt-dlp)
  ├─▶ SummarizeActivity (pi/agy CLI)
  └─▶ Save result to SQLite
```

## Database

Two SQLite databases in `DATA_DIR`:
- `summarize.db` — app state (runs, results, transcripts)
- `workflows.db` — go-workflows state

## Testing

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package
go test ./internal/store
```

## Limitations (v1)

- YouTube videos without captions fail (no audio transcription fallback)
- Long transcripts are truncated (not chunked)
- No authentication
- No streaming/SSE (polling only)
- No cancellation endpoint

## License

MIT
