// Package store provides SQLite database access for the summarize service.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jimbo/summarize/internal/domain"
)

//go:embed schema.sql
var schemaSQL string

const runColumns = `
	id, status, stage, input_type,
	source_url, input_text,
	youtube_video_id, youtube_title,
	engine, model, prompt,
	format, truncated,
	transcript, transcript_chars,
	transcript_with_timestamps, transcript_with_timestamps_chars,
	summary, summary_chars,
	error_code, error_message,
	stderr, command,
	event_id, event_published_at,
	workflow_instance_id, workflow_execution_id,
	created_at, updated_at, started_at, finished_at
`

// Store wraps the SQLite database connection.
type Store struct {
	db *sql.DB
}

// New opens a SQLite database at the given path and configures it.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// SQLite best practices: single writer, WAL mode, busy timeout
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	// Run schema
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("run schema: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN model TEXT"); err != nil && !isDuplicateColumnError(err) {
		db.Close()
		return nil, fmt.Errorf("migrate model column: %w", err)
	}

	if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN format TEXT NOT NULL DEFAULT 'summary'"); err != nil && !isDuplicateColumnError(err) {
		db.Close()
		return nil, fmt.Errorf("migrate format column: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN truncated BOOLEAN NOT NULL DEFAULT 0"); err != nil && !isDuplicateColumnError(err) {
		db.Close()
		return nil, fmt.Errorf("migrate truncated column: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN transcript_with_timestamps TEXT"); err != nil && !isDuplicateColumnError(err) {
		db.Close()
		return nil, fmt.Errorf("migrate transcript_with_timestamps column: %w", err)
	}
	if _, err := db.Exec("ALTER TABLE summary_runs ADD COLUMN transcript_with_timestamps_chars INTEGER NOT NULL DEFAULT 0"); err != nil && !isDuplicateColumnError(err) {
		db.Close()
		return nil, fmt.Errorf("migrate transcript_with_timestamps_chars column: %w", err)
	}

	// Create index on format column (after migration ensures column exists)
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_summary_runs_format ON summary_runs(format)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("create format index: %w", err)
	}

	// Create index for YouTube dedup lookups (video_id + key parameters)
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_summary_runs_youtube_dedup ON summary_runs(youtube_video_id, status, format, engine, model)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("create youtube dedup index: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateRun inserts a new queued run.
func (s *Store) CreateRun(run *domain.Run) error {
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = now
	}

	_, err := s.db.Exec(`
		INSERT INTO summary_runs (
			id, status, stage, input_type, source_url, input_text,
			youtube_video_id, youtube_title, engine, model, prompt,
			format, truncated,
			transcript, transcript_chars,
			transcript_with_timestamps, transcript_with_timestamps_chars,
			summary, summary_chars,
			error_code, error_message, stderr, command,
			event_id, event_published_at,
			workflow_instance_id, workflow_execution_id,
			created_at, updated_at, started_at, finished_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?,
			?, ?,
			?, ?,
			?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?,
			?, ?, ?, ?
		)`,
		run.ID, run.Status, run.Stage, run.InputType, nullStr(run.SourceURL), nullStr(run.InputText),
		nullStr(run.YouTubeVideoID), nullStr(run.YouTubeTitle), run.Engine, nullStr(run.Model), run.Prompt,
		run.Format, boolInt(run.Truncated),
		nullStr(run.Transcript), run.TranscriptChars,
		nullStr(run.TranscriptWithTimestamps), run.TranscriptWithTimestampsChars,
		nullStr(run.Summary), run.SummaryChars,
		nullStr(run.ErrorCode), nullStr(run.ErrorMessage), nullStr(run.Stderr), nullStr(run.Command),
		nullStr(run.EventID), nullTime(run.EventPublishedAt),
		nullStr(run.WorkflowInstanceID), nullStr(run.WorkflowExecutionID),
		run.CreatedAt.Format(time.RFC3339Nano), run.UpdatedAt.Format(time.RFC3339Nano), nullTime(run.StartedAt), nullTime(run.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// GetRun retrieves a run by ID.
func (s *Store) GetRun(id string) (*domain.Run, error) {
	row := s.db.QueryRow("SELECT "+runColumns+" FROM summary_runs WHERE id = ?", id)
	return scanRun(row)
}

// MarkRunning sets status=running, stage=starting, started_at.
func (s *Store) MarkRunning(id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET status = 'running', stage = 'starting', started_at = ?, updated_at = ?
		WHERE id = ?`,
		now, now, id,
	)
	return err
}

// SetStage updates the stage field.
func (s *Store) SetStage(id, stage string) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs SET stage = ?, updated_at = ? WHERE id = ?`,
		stage, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// UpdateEventPublished marks the event as published.
func (s *Store) UpdateEventPublished(id, eventID string, publishedAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs SET event_id = ?, event_published_at = ?, updated_at = ? WHERE id = ?`,
		eventID, publishedAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// UpdateWorkflowIDs stores workflow instance and execution IDs.
func (s *Store) UpdateWorkflowIDs(id, instanceID, executionID string) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET workflow_instance_id = ?, workflow_execution_id = ?, updated_at = ?
		WHERE id = ?`,
		instanceID, executionID, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// SaveTranscript stores the fetched transcript and YouTube metadata.
func (s *Store) SaveTranscript(id, videoID, title, transcript string) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET youtube_video_id = ?, youtube_title = ?, transcript = ?, transcript_chars = ?,
		    updated_at = ?
		WHERE id = ?`,
		videoID, title, transcript, len(transcript), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// SaveSucceeded marks the run as succeeded with the summary.
func (s *Store) SaveSucceeded(id, summary, stderr, command string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET status = 'succeeded', stage = 'done', summary = ?, summary_chars = ?,
		    stderr = ?, command = ?, finished_at = ?, updated_at = ?
		WHERE id = ?`,
		summary, len(summary), stderr, command, now, now, id,
	)
	return err
}

// SaveFailed marks the run as failed with error details.
func (s *Store) SaveFailed(id, errorCode, errorMessage, stderr, command string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET status = 'failed', stage = 'failed',
		    error_code = ?, error_message = ?, stderr = ?, command = ?,
		    finished_at = ?, updated_at = ?
		WHERE id = ?`,
		errorCode, errorMessage, stderr, command, now, now, id,
	)
	return err
}

// FailRunIfNotTerminal marks a run as failed only if it hasn't already reached a terminal state.
func (s *Store) FailRunIfNotTerminal(id, errorCode, errorMessage string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET status = 'failed', stage = 'failed',
		    error_code = ?, error_message = ?,
		    finished_at = ?, updated_at = ?
		WHERE id = ? AND status NOT IN ('succeeded', 'failed')`,
		errorCode, errorMessage, now, now, id,
	)
	return err
}

// FindQueuedRunsWithoutWorkflow returns runs that are queued but have no workflow started.
func (s *Store) FindQueuedRunsWithoutWorkflow() ([]domain.Run, error) {
	rows, err := s.db.Query(`
		SELECT id FROM summary_runs
		WHERE status = 'queued' AND workflow_instance_id IS NULL OR workflow_instance_id = ''
		ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []domain.Run
	for rows.Next() {
		var r domain.Run
		if err := rows.Scan(&r.ID); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// FindCachedRun returns the most recent successful run for the given video, format, engine, and model
// created after the given since time. Returns nil if no matching run exists.
func (s *Store) FindCachedRun(videoID, format, engine, model string, since time.Time) (*domain.Run, error) {
	sinceStr := since.Format(time.RFC3339Nano)
	row := s.db.QueryRow(`
		SELECT `+runColumns+` FROM summary_runs
		WHERE youtube_video_id = ?
		  AND status = 'succeeded'
		  AND format = ?
		  AND engine = ?
		  AND model = ?
		  AND created_at >= ?
		ORDER BY created_at DESC
		LIMIT 1`,
		videoID, format, engine, model, sinceStr,
	)
	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

// FindInFlightRun returns the most recent in-flight (queued or running) run
// for the given video, format, engine, and model. Returns nil if none exists.
func (s *Store) FindInFlightRun(videoID, format, engine, model string) (*domain.Run, error) {
	row := s.db.QueryRow(`
		SELECT `+runColumns+` FROM summary_runs
		WHERE youtube_video_id = ?
		  AND status IN ('queued', 'running')
		  AND format = ?
		  AND engine = ?
		  AND model = ?
		ORDER BY created_at DESC
		LIMIT 1`,
		videoID, format, engine, model,
	)
	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

// scanRun scans a row into a Run.
func scanRun(row *sql.Row) (*domain.Run, error) {
	var r domain.Run
	var sourceURL, inputText, videoID, title sql.NullString
	var model sql.NullString
	var tsTranscript sql.NullString
	var transcript, summary sql.NullString
	var errorCode, errorMessage, stderr, command sql.NullString
	var eventID, wfInstID, wfExecID sql.NullString
	var eventPublishedAt, startedAt, finishedAt sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(
		&r.ID, &r.Status, &r.Stage, &r.InputType,
		&sourceURL, &inputText,
		&videoID, &title,
		&r.Engine, &model, &r.Prompt,
		&r.Format, &r.Truncated,
		&transcript, &r.TranscriptChars,
		&tsTranscript, &r.TranscriptWithTimestampsChars,
		&summary, &r.SummaryChars,
		&errorCode, &errorMessage,
		&stderr, &command,
		&eventID, &eventPublishedAt,
		&wfInstID, &wfExecID,
		&createdAt, &updatedAt, &startedAt, &finishedAt,
	)
	if err != nil {
		return nil, err
	}

	r.SourceURL = sourceURL.String
	r.InputText = inputText.String
	r.YouTubeVideoID = videoID.String
	r.YouTubeTitle = title.String
	r.Model = model.String
	r.Transcript = transcript.String
	r.Summary = summary.String
	r.ErrorCode = errorCode.String
	r.ErrorMessage = errorMessage.String
	r.Stderr = stderr.String
	r.Command = command.String
	r.EventID = eventID.String
	r.EventPublishedAt = parseTime(eventPublishedAt.String)
	r.WorkflowInstanceID = wfInstID.String
	r.WorkflowExecutionID = wfExecID.String
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	r.StartedAt = parseTime(startedAt.String)
	r.FinishedAt = parseTime(finishedAt.String)

	r.TranscriptWithTimestamps = tsTranscript.String

	return &r, nil
}

// SaveTranscriptWithTimestamps stores the timestamped transcript for a run.
func (s *Store) SaveTranscriptWithTimestamps(id, tsTranscript string) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET transcript_with_timestamps = ?, transcript_with_timestamps_chars = ?,
		    updated_at = ?
		WHERE id = ?`,
		tsTranscript, len(tsTranscript), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// SaveTruncated updates the truncated flag for a run.
func (s *Store) SaveTruncated(id string, truncated bool) error {
	_, err := s.db.Exec(`
		UPDATE summary_runs
		SET truncated = ?, updated_at = ?
		WHERE id = ?`,
		boolInt(truncated), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339Nano), Valid: true}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
