package events

import (
	"encoding/json"
	"time"
)

// SummaryRequested is the payload published to NATS when a new summary is requested.
type SummaryRequested struct {
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Marshal returns the JSON bytes of the event.
func (e *SummaryRequested) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalSummaryRequested parses a JSON byte slice into a SummaryRequested event.
func UnmarshalSummaryRequested(data []byte) (*SummaryRequested, error) {
	var e SummaryRequested
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
