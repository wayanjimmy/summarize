package events

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// Publisher publishes events to NATS.
type Publisher struct {
	nc      *nats.Conn
	subject string
	log     *slog.Logger
}

// NewPublisher creates a new Publisher.
func NewPublisher(nc *nats.Conn, subject string) *Publisher {
	return &Publisher{
		nc:      nc,
		subject: subject,
		log:     slog.With("component", "events.publisher"),
	}
}

// PublishSummaryRequested publishes a new summary request event.
func (p *Publisher) PublishSummaryRequested(runID string) (*SummaryRequested, error) {
	evt := &SummaryRequested{
		EventID:   uuid.NewString(),
		EventType: "summary.requested",
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
	}

	data, err := evt.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}

	if err := p.nc.Publish(p.subject, data); err != nil {
		return nil, fmt.Errorf("publish to %s: %w", p.subject, err)
	}

	p.log.Info("Published summary.requested",
		"run_id", runID,
		"event_id", evt.EventID,
	)

	return evt, nil
}
