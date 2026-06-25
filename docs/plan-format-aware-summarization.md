# Format-Aware Summarization — Implementation Plan

## Overview

Add multi-format output support to the summarize service, allowing API clients to request different output shapes (chapters, threads, blog posts) beyond the current plain summary. Inspired by the Hermes agent's SKILL.md-driven approach but backed by our durable workflow engine.

**Core insight:** Our service's value is *predictable, durable execution*. Formats make the output more versatile without sacrificing that strength.

---

## Design Decisions (9 branches resolved)

| # | Decision | Resolution |
|---|----------|------------|
| 1 | Format selection mechanism | Fixed enum, not freeform |
| 2 | Output shape in response | Single `summary` string field |
| 3 | Format set for v1 | 4 formats: `summary` (default), `chapters`, `thread`, `blog` |
| 4 | Timestamp plumbing | Dual transcript storage (`transcript` + `transcript_with_timestamps`) |
| 5 | Prompt composition | Format template replaces default; user `prompt` appends as refinement |
| 6 | Long video handling | Hard truncation stays; explicit `truncated` flag; reject `chapters` for long videos |
| 7 | Invalid input+format combos | Validate and reject with 400 |
| 8 | Template architecture | Base template (grounding rules) + shape-specific sections with rich prompting |
| 9 | Schema design | First-class columns for `format`, `truncated`, `transcript_with_timestamps` |

---

## Phase 1: Schema & Domain Layer

### 1.1 Schema migration (`internal/store/schema.sql`)

Append these `ALTER TABLE` statements (guarded by `IF NOT EXISTS` pattern via `isDuplicateColumnError`):

```sql
ALTER TABLE summary_runs ADD COLUMN format TEXT NOT NULL DEFAULT 'summary';

ALTER TABLE summary_runs ADD COLUMN truncated BOOLEAN NOT NULL DEFAULT 0;

ALTER TABLE summary_runs ADD COLUMN transcript_with_timestamps TEXT;

ALTER TABLE summary_runs ADD COLUMN transcript_with_timestamps_chars INTEGER NOT NULL DEFAULT 0;
```

Register them in `store.New()` the same way `model` is currently migrated:
```go
if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN format TEXT NOT NULL DEFAULT 'summary'"); err != nil && !isDuplicateColumnError(err) {
    ...
}
// ... etc for each column
```

### 1.2 Domain types (`internal/domain/types.go`)

Add format constants alongside existing constants:

```go
// Format constants for output shape.
const (
    FormatSummary  = "summary"
    FormatChapters = "chapters"
    FormatThread   = "thread"
    FormatBlog     = "blog"
)

// ValidFormats is the set of accepted format values.
var ValidFormats = map[string]bool{
    FormatSummary:  true,
    FormatChapters: true,
    FormatThread:   true,
    FormatBlog:     true,
}
```

Add 4 fields to `Run` struct:

```go
type Run struct {
    // ... existing fields ...

    Format                        string  // "summary", "chapters", "thread", "blog"
    Truncated                     bool    // whether transcript was truncated
    TranscriptWithTimestamps      string  // VTT-style transcript with time cues
    TranscriptWithTimestampsChars int     // character count
}
```

### 1.3 Store layer (`internal/store/store.go`)

Update these areas:

- **`runColumns`**: Add `format`, `truncated`, `transcript_with_timestamps`, `transcript_with_timestamps_chars`
- **`scanRun()`**: Add variables + scan targets for the 4 new columns
- **`CreateRun()`**: Add new columns to INSERT
- **New method `SaveTranscriptWithTimestamps(id, tsTranscript string) error`**: Updates the timestamped transcript column
- **New method `SaveTruncated(id string, truncated bool) error`**: Updates the truncated flag
- **`SaveTranscript()`**: No changes needed (still saves plain text transcript)

---

## Phase 2: VTT Timestamp Parsing

### 2.1 New function in `internal/youtube/vtt.go`

```go
// ParseVTTWithTimestamps parses WebVTT into timestamped text.
// Output format: "MM:SS text of that segment\n..."
func ParseVTTWithTimestamps(data string) string
```

This is like `ParseVTT()` but **preserves timestamps** instead of discarding them:

- When a VTT timestamp line is matched, extract the start time (e.g. `00:00:05.000`)
- Format it as `MM:SS` or `H:MM:SS`
- Prepend it to the next text segment
- Deduplicate consecutive identical lines
- Output: newline-separated `"MM:SS text"` lines

### 2.2 New function in `internal/youtube/ytdlp.go` (or `youtube.go`)

After fetching raw VTT content, parse it twice:

```go
// Current: result.Transcript = ParseVTT(vttData)
// Add:     result.TranscriptWithTimestamps = ParseVTTWithTimestamps(vttData)
```

Update the `TranscriptResult` struct:

```go
type TranscriptResult struct {
    VideoID                  string
    Title                    string
    Transcript               string // plain text (existing)
    TranscriptWithTimestamps  string // timestamped text (new)
}
```

---

## Phase 3: Prompt Architecture

### 3.1 Package layout (`internal/prompt/`)

```
internal/prompt/
├── prompt.go          # existing: Build(), BuildYouTube(), Truncate() (refactor in 3.4)
├── base.go            # NEW: grounding rules, source block builder
├── summary.go         # NEW: summaryShape()
├── chapters.go        # NEW: chaptersShape()
├── thread.go          # NEW: threadShape()
├── blog.go            # NEW: blogShape()
├── build.go           # NEW: BuildForFormat() — the main entry point
├── prompt_test.go     # existing tests (keep)
└── templates_test.go  # NEW: golden-file tests per format
```

### 3.2 Base template (`internal/prompt/base.go`)

```go
// groundingRules returns quality rules shared across all formats.
func groundingRules() string {
    return `Rules:
- Do NOT fabricate facts, quotes, or claims not present in the source
- Preserve technical terminology exactly as used
- Be concise but do not omit key points
- If the source has timestamps, use them to locate relevant sections`
}

// sourceBlock builds the metadata + content footer.
func sourceBlock(inputType, title, url, content string) string {
    // Builds "Source type: ...\nTitle: ...\nURL: ...\n\nContent:\n..."
}
```

### 3.3 Format shape functions

Each returns a string with: shape description, explicit rules, and a short example.

#### `summary.go` — `summaryShape()`
Backward-compatible with the current `DefaultPrompt`. Produce a clear, structured summary.

#### `chapters.go` — `chaptersShape()`
```
Produce timestamped chapter markers from this content.

Rules:
- Group content by topic shifts into 5–15 chapters
- Each chapter MUST start with a timestamp (MM:SS or H:MM:SS)
- Format: "MM:SS Chapter Title — brief description of what's covered"
- The first chapter should cover the introduction/opening
- The final chapter should cover conclusions or closing remarks
- Chapter titles should be descriptive (3–8 words)
- Do not invent timestamps; use ones from the source content

Example:
00:00 Introduction — host introduces the problem of unreliable AI outputs
03:45 Background — overview of existing approaches and their limitations
12:30 Methodology — how they built their evaluation framework
...
```

#### `thread.go` — `threadShape()`
```
Produce a Twitter/X thread from this content.

Rules:
- Each post MUST be self-contained (readable without other posts)
- Maximum 280 characters per post (including the "N/" prefix)
- Number each post sequentially: "1/", "2/", "3/"
- Aim for 8–12 posts for short content, up to 20 for long content
- First post: hook the reader with the most surprising/interesting finding
- Final post: key takeaway or call to action
- Do not fabricate claims not in the source

Example:
1/ We studied 10,000 AI outputs and found something surprising about prompt length. 🧵

2/ Shorter prompts actually produced HIGHER quality summaries — but only for videos under 10 minutes.
...
```

#### `blog.go` — `blogShape()`
```
Produce a blog post article from this content.

Rules:
- Include a compelling title (not just the video title)
- Structure with H2/H3 headings (## and ### in markdown)
- Include a brief intro paragraph that hooks the reader
- Group content into logical sections with descriptive headings
- End with a "Key Takeaways" section (3–5 bullet points)
- Use markdown formatting (bold, lists, emphasis) for readability
- Do not fabricate claims not in the source

Example:
# How We Cut AI Costs by 60% Without Losing Quality

## The Problem
After six months of running our summarization pipeline...

## What We Tried
### Approach 1: Smaller Models
...

## Key Takeaways
- Smaller models work for short content but fail on nuance
- Caching cuts costs more than model downsizing
- ...
```

### 3.4 New entry point (`internal/prompt/build.go`)

```go
// BuildForFormat composes the full prompt based on format and source type.
// - format: one of the format constants ("summary", "chapters", etc.)
// - refinement: user's optional custom instructions (appended after template)
// - inputType: "youtube" or "text"
// - title, url: metadata (may be empty for text input)
// - content: the transcript or text to summarize
func BuildForFormat(format, refinement, inputType, title, url, content string) string
```

Composition order:
```
1. Format shape instructions (summaryShape, chaptersShape, etc.)
2. Grounding rules (from base.go)
3. User refinement (if non-empty): "\nAdditional instructions: {refinement}"
4. Source block: source type, title, url, content
```

### 3.5 Backward compatibility

When `format` is `"summary"` (default), `BuildForFormat` produces output equivalent to the current `BuildYouTube`/`Build` functions. The existing `DefaultPrompt` config is only used when `format` is `"summary"` **and** the user didn't provide a `prompt` override — preserving existing behavior exactly.

---

## Phase 4: API Layer Changes

### 4.1 Request struct (`internal/httpapi/handlers.go`)

Add `Format` field:

```go
type createSummaryRequest struct {
    URL    string `json:"url"`
    Text   string `json:"text"`
    Engine string `json:"engine"`
    Model  string `json:"model"`
    Prompt string `json:"prompt"`
    Format string `json:"format"` // NEW: "summary", "chapters", "thread", "blog"
}
```

### 4.2 Validation in `CreateSummary()` handler

After existing validation, add format validation:

```go
// Validate format
format := req.Format
if format == "" {
    format = domain.FormatSummary  // default
}
if !domain.ValidFormats[format] {
    writeError(w, http.StatusBadRequest, "invalid_request",
        fmt.Sprintf("format must be one of: summary, chapters, thread, blog"))
    return
}

// Validate format+input compatibility
if format == domain.FormatChapters && inputType == domain.InputTypeText {
    writeError(w, http.StatusBadRequest, "invalid_request",
        "format 'chapters' requires a YouTube URL")
    return
}
```

Set `format` on the `Run`:
```go
run := &domain.Run{
    Format: format,
    // ... existing fields ...
}
```

### 4.3 Prompt resolution changes

Current logic:
```go
promptText := h.Config.DefaultPrompt
if req.Prompt != "" { promptText = req.Prompt }
```

New logic:
```go
// For format != "summary", the format template takes over.
// User prompt becomes a refinement.
// For format == "summary", keep existing behavior.
promptText := ""
if format == domain.FormatSummary {
    promptText = h.Config.DefaultPrompt
}
if req.Prompt != "" {
    promptText = req.Prompt
}
```

### 4.4 Response structs (`internal/httpapi/responses.go`)

Add `Format` and `Truncated` fields to `SummaryResponse`:

```go
type SummaryResponse struct {
    // ... existing fields ...
    Format    string `json:"format"`              // NEW
    Truncated bool   `json:"truncated,omitempty"` // NEW
}
```

Update `GetSummary()` to include `run.Format` and `run.Truncated` in the response.

---

## Phase 5: Workflow Activity Changes

### 5.1 `FetchTranscriptActivity` (`internal/workflow/activities.go`)

After fetching VTT and parsing, store both transcripts:

```go
result, err := youtube.FetchTranscript(ctx, cfg, run.SourceURL)
// ... error handling ...

// Save plain transcript (existing)
if err := deps.Store.SaveTranscript(runID, result.VideoID, result.Title, result.Transcript); err != nil {
    return err
}

// Save timestamped transcript (new)
if err := deps.Store.SaveTranscriptWithTimestamps(runID, result.TranscriptWithTimestamps); err != nil {
    return err
}
```

### 5.2 `SummarizeActivity` (`internal/workflow/activities.go`)

Replace the current prompt-building logic:

```go
// Select content based on format
var content string
switch run.InputType {
case domain.InputTypeYouTube:
    if run.Format == domain.FormatChapters {
        content = run.TranscriptWithTimestamps
    } else {
        content = run.Transcript
    }
case domain.InputTypeText:
    content = run.InputText
}

// Truncate if too large
var truncated bool
content, truncated = prompt.Truncate(content, deps.Config.MaxInputChars)

// Save truncated flag
if truncated {
    _ = deps.Store.SaveTruncated(runID, true)
}

// Build prompt using format-aware builder
fullPrompt := prompt.BuildForFormat(run.Format, run.Prompt, run.InputType, run.YouTubeTitle, run.SourceURL, content)
```

### 5.3 `chapters` + long video rejection

This validation happens **at summary time** (not at creation time, because we don't know the video length until after fetching the transcript). In `SummarizeActivity`, before building the prompt:

```go
if run.Format == domain.FormatChapters && len(run.TranscriptWithTimestamps) > deps.Config.MaxInputChars {
    return fmt.Errorf("chapters_unsupported_length: video too long for chapters format")
}
```

This should trigger `FailRunActivity` with a clear error code so the client understands the rejection.

Alternatively, this can be handled in `FetchTranscriptActivity` once the transcript length is known, preventing wasted engine calls. **Preferred: reject in `FetchTranscriptActivity`** — fail fast, before `SummarizeActivity`.

---

## Phase 6: Testing

### 6.1 VTT parsing tests (`internal/youtube/vtt_test.go`)

Add test cases for `ParseVTTWithTimestamps`:

- Standard VTT with timestamps → `"MM:SS text\n..."` output
- VTT with HTML tags → tags stripped, timestamps preserved
- VTT with duplicated lines → deduplicated
- Edge cases: empty input, header-only, malformed timestamps

### 6.2 Prompt template tests (`internal/prompt/templates_test.go`)

Golden-file tests for each format:

- `testdata/summary_golden.txt` — expected prompt for summary format
- `testdata/chapters_golden.txt` — expected prompt for chapters format
- `testdata/thread_golden.txt` — expected prompt for thread format
- `testdata/blog_golden.txt` — expected prompt for blog format

Each test feeds a short sample transcript and asserts the composed prompt matches the golden file.

### 6.3 Handler tests (`internal/httpapi/handlers_test.go`)

- `format` field acceptance (valid values, default to summary)
- Invalid format → 400
- `chapters` + text input → 400 with correct error message
- `chapters` + youtube → accepted
- Omitted `format` → backward compatible behavior

### 6.4 Hurl integration tests

New test file `tests/create_youtube_chapters.hurl`:
```
POST http://localhost:8080/v1/summaries
Content-Type: application/json

{
  "url": "https://www.youtube.com/watch?v=...",
  "format": "chapters"
}

HTTP 202
[Asserts]
header "Content-Type" == "application/json"
jsonpath "$.format" == "chapters"
```

New test file `tests/format_validation.hurl`:
```
# Test: chapters + text → 400
POST http://localhost:8080/v1/summaries
Content-Type: application/json
{
  "text": "some long text here",
  "format": "chapters"
}
HTTP 400
[Asserts]
jsonpath "$.error.code" == "invalid_request"
```

---

## Phase 7: Documentation

- Update README.md with `format` field in API docs
- Document valid format values and their constraints
- Document validation rules (chapters requires YouTube, etc.)
- Add examples for each format in curl/HTTP format

---

## File Change Summary

| File | Action | Description |
|------|--------|-------------|
| `internal/store/schema.sql` | Edit | Add 4 ALTER TABLE statements |
| `internal/store/store.go` | Edit | Add columns to queries, scanning, inserts; add `SaveTranscriptWithTimestamps`, `SaveTruncated` |
| `internal/domain/types.go` | Edit | Add format constants, ValidFormats map, 4 new Run fields |
| `internal/youtube/vtt.go` | Edit | Add `ParseVTTWithTimestamps()` function |
| `internal/youtube/vtt_test.go` | Edit | Add tests for `ParseVTTWithTimestamps` |
| `internal/youtube/ytdlp.go` | Edit | Update `TranscriptResult` struct, call both parsers |
| `internal/prompt/base.go` | **New** | Grounding rules, source block builder |
| `internal/prompt/summary.go` | **New** | `summaryShape()` |
| `internal/prompt/chapters.go` | **New** | `chaptersShape()` |
| `internal/prompt/thread.go` | **New** | `threadShape()` |
| `internal/prompt/blog.go` | **New** | `blogShape()` |
| `internal/prompt/build.go` | **New** | `BuildForFormat()` entry point |
| `internal/prompt/prompt.go` | Edit | Keep existing functions for backward compat (or thin wrappers) |
| `internal/prompt/templates_test.go` | **New** | Golden-file tests per format |
| `internal/httpapi/handlers.go` | Edit | Add `Format` to request struct, validation, prompt resolution |
| `internal/httpapi/responses.go` | Edit | Add `Format`, `Truncated` to `SummaryResponse` |
| `internal/httpapi/handlers_test.go` | Edit | Add format validation tests |
| `internal/workflow/activities.go` | Edit | Dual transcript storage, format-aware prompt building |
| `tests/create_youtube_chapters.hurl` | **New** | Chapters integration test |
| `tests/format_validation.hurl` | **New** | Format validation integration test |
| `README.md` | Edit | API documentation update |

---

## What's Deferred (v2)

| Item | Rationale |
|------|-----------|
| **Chunk-and-merge** for long videos | Multi-week effort; deserves its own focus. Chapters format handles this by rejecting long videos for now |
| **Structured JSON output** per format | Fragile across models; string output is sufficient when clients can parse |
| **Additional formats** (`sections`, `quotes`, `chapter_summaries`) | Add if there's demand; base+shape architecture makes it trivial |
| **Cost accounting** per format | Blocked on chunk-and-merge (cost multiplier) |
| **`youtube-transcript-api` migration** (replace yt-dlp) | Current approach works and gives metadata for free. Revisit if yt-dlp becomes a maintenance burden |

---

## Implementation Order (recommended)

1. **Schema + domain** (Phase 1) — foundation, no breaking changes
2. **VTT timestamp parsing** (Phase 2) — standalone, testable in isolation
3. **Prompt architecture** (Phase 3) — the creative core, benefits from iteration
4. **Store updates** (Phase 5.1 store changes) — wire up persistence
5. **Workflow updates** (Phase 5) — connect the pipeline
6. **API layer** (Phase 4) — expose to clients
7. **Tests** (Phase 6) — validate everything
8. **Docs** (Phase 7) — update user-facing docs

Steps 1–3 can be done in parallel with minimal merge conflicts. Steps 4–6 are sequential (each builds on the prior). Step 7 runs alongside 4–6. Step 8 is last.
