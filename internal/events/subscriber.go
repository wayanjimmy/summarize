package events

import (
	"log/slog"

	"github.com/nats-io/nats.go"
)

// Handler is called when a summary.requested event is received.
type Handler func(evt *SummaryRequested)

// Subscriber listens for summary request events.
type Subscriber struct {
	nc       *nats.Conn
	subject  string
	handler  Handler
	sub      *nats.Subscription
	log      *slog.Logger
}

// NewSubscriber creates a new Subscriber.
func NewSubscriber(nc *nats.Conn, subject string, handler Handler) *Subscriber {
	return &Subscriber{
		nc:      nc,
		subject: subject,
		handler: handler,
		log:     slog.With("component", "events.subscriber"),
	}
}

// Start begins listening for events.
func (s *Subscriber) Start() error {
	var err error
	s.sub, err = s.nc.Subscribe(s.subject, func(msg *nats.Msg) {
		evt, err := UnmarshalSummaryRequested(msg.Data)
		if err != nil {
			s.log.Error("Failed to unmarshal event", "error", err)
			return
		}
		s.handler(evt)
	})
	if err != nil {
		return err
	}
	s.log.Info("Subscribed", "subject", s.subject)
	return nil
}

// Stop unsubscribes.
func (s *Subscriber) Stop() {
	if s.sub != nil {
		s.sub.Unsubscribe()
	}
}
