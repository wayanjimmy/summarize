---
name: summarize
description: |-
  Summarize YouTube videos or text using a local REST API backed by `agy` and `pi` CLI engines.
  USE FOR: summarizing videos, summarizing YouTube URLs, summarizing text, chapter extraction, thread generation, blog post generation, fetching video transcripts.
  DO NOT USE FOR: general coding questions, editing files, or tasks unrelated to the summarize service.
---

# summarize — YouTube & Text Summarization Service

## Base URL

The service base URL is **read from the environment**, not hardcoded.

- Env var: `SUMMARIZE_BASE_URL`
- Default (when unset): `http://127.0.0.1:8420`

Resolve it before issuing requests:

```bash
BASE_URL="${SUMMARIZE_BASE_URL:-http://127.0.0.1:8420}"
```

> If `SUMMARIZE_BASE_URL` is set, always use that value verbatim (it may include a path prefix or scheme override). Do not assume `http://127.0.0.1:8420`.

The summarize service is a local REST API that takes a YouTube URL or raw text and returns a summary using an LLM engine (`agy` or `pi`). It runs as a user-level systemd service.

## When to use this skill

Use this skill when the user asks to summarize a YouTube video or text. The service handles:
- YouTube video transcript extraction (via `yt-dlp`)
- Raw text summarization
- Four output formats: `summary`, `chapters`, `thread`, `blog`
- Two engine backends: `agy` (fast, gemini models) and `pi` (pi CLI models)
- Async job queuing with polling
- Caching and dedup for repeated requests

## Quick Start

```bash
BASE_URL="${SUMMARIZE_BASE_URL:-http://127.0.0.1:8420}"
```

### 1. Check service health

```bash
curl -s "$BASE_URL/healthz"
# {"ok":true}
```

### 2. List available models

```bash
curl -s "$BASE_URL/v1/models"
```

### 3. Submit a video summary

```bash
curl -s -X POST "$BASE_URL/v1/summaries" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://youtu.be/VIDEO_ID",
    "engine": "agy",
    "format": "summary"
  }'
```

Returns a `run_id` immediately (202 Accepted):

```json
{"run_id":"<UUID>","status":"queued","status_url":"/v1/runs/<UUID>/status","result_url":"/v1/summaries/<UUID>"}
```

### 4. Poll for completion

```bash
curl -s "$BASE_URL/v1/runs/<RUN_ID>/status"
```

Stages: `queued` → `running` (fetching_transcript → summarizing) → `succeeded` / `failed`

### 5. Fetch the result

```bash
curl -s "$BASE_URL/v1/summaries/<RUN_ID>"
```

## API Reference

### POST /v1/summaries — Create a summary

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `url` | One of `url`/`text` | string | YouTube video URL |
| `text` | One of `url`/`text` | string | Raw text to summarize |
| `engine` | No | string | `"agy"` or `"pi"` (default from service config) |
| `model` | No | string | Specific model (validated against runtime catalog; omit for default) |
| `format` | No | string | `"summary"` (default), `"chapters"`, `"thread"`, `"blog"` |
| `prompt` | No | string | Custom instruction (max 20K chars; overrides default prompt) |

**Engine defaults:**
- `agy`: `gemini-3.5-flash-low` (gemini models)
- `pi`: `cpa/glm-5-turbo` (pi CLI proxy models)

**Output formats:**

| Format | Description | YouTube Required? |
|--------|-------------|-------------------|
| `summary` | Structured overview + key points + action items | No |
| `chapters` | Timestamped chapter markers | Yes |
| `thread` | Twitter/X thread (280 chars/post) | No |
| `blog` | Blog post with headings and takeaways | No |

### GET /v1/runs/{run_id}/status — Poll status

Response:
```json
{
  "run_id": "<UUID>",
  "status": "running",
  "stage": "summarizing",
  "created_at": "2026-07-30T02:43:29Z",
  "updated_at": "2026-07-30T02:43:40Z"
}
```

Status values: `queued`, `running`, `succeeded`, `failed`

### GET /v1/summaries/{run_id} — Fetch result

Response (succeeded):
```json
{
  "run_id": "<UUID>",
  "status": "succeeded",
  "stage": "done",
  "input_type": "youtube",
  "source_url": "https://youtu.be/...",
  "youtube": { "video_id": "...", "title": "..." },
  "engine": "agy",
  "format": "summary",
  "summary": "Here is the summary...",
  "transcript_chars": 47129,
  "summary_chars": 4243,
  "created_at": "...",
  "started_at": "...",
  "finished_at": "..."
}
```

## End-to-end flow (for agent automation)

```bash
BASE_URL="${SUMMARIZE_BASE_URL:-http://127.0.0.1:8420}"

# Step 1: Submit
BODY=$(curl -s -X POST "$BASE_URL/v1/summaries" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://youtu.be/VIDEO_ID","engine":"agy","format":"summary"}')
RUN_ID=$(echo "$BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])")

# Step 2: Poll until terminal
for i in $(seq 1 60); do
  STATUS=$(curl -s "$BASE_URL/v1/runs/$RUN_ID/status" | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
  [ "$STATUS" = "succeeded" ] || [ "$STATUS" = "failed" ] && break
  sleep 5
done

# Step 3: Fetch result
curl -s "$BASE_URL/v1/summaries/$RUN_ID"
```

## Caching & Dedup

- Same YouTube video + format + engine + model = **cached result** within TTL
- In-flight requests for the same parameters are deduped (no duplicate workflows)
- Custom prompts bypass the cache
- Raw text inputs are never cached

## Service Management

```bash
# Status
systemctl --user status summarize.service

# Restart (after config changes)
systemctl --user restart summarize.service

# Stream logs
journalctl --user -u summarize.service -f

# Config
~/.config/systemd/user/summarize.env
```

## Troubleshooting

| Symptom | Likely cause |
|---------|-------------|
| `engine_failed: pi exited with status 1` | pi model unavailable or incompatible; switch to `agy` |
| `engine_failed: agy exited with status 1` | agy binary/API issue; check `journalctl --user -u summarize.service -f` |
| `transcript_unavailable` | Video has no captions; no audio-transcription fallback |
| Outcome submission returns 401 | Product key expired or mismatched; key must be rotated in `summarize.env` |
| Poll returns 404 | Run ID is wrong or already expired |
| Model validation returns 400 | Model name not in runtime catalog; list models with `GET /v1/models` |
