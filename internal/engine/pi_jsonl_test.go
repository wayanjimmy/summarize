package engine

import (
	"bufio"
	"strings"
	"testing"
)

func TestParsePiJSONL_textDelta(t *testing.T) {
	// Simulate JSONL with text_delta events
	input := `{"type":"session","id":"sess1"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"assistant","model":"test-model"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hello "}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"world!"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":""}]}}`

	scanner := bufio.NewScanner(strings.NewReader(input))
	text, err := parsePiJSONL(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello world!" {
		t.Errorf("got %q, want %q", text, "Hello world!")
	}
}

func TestParsePiJSONL_textEnd(t *testing.T) {
	// text_end should take priority over accumulated deltas
	input := `{"type":"session","id":"sess1"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_end","text":"Full authoritative text"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":""}]}}`

	scanner := bufio.NewScanner(strings.NewReader(input))
	text, err := parsePiJSONL(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Full authoritative text" {
		t.Errorf("got %q, want %q", text, "Full authoritative text")
	}
}

func TestParsePiJSONL_messageEndFallback(t *testing.T) {
	// Fallback to message_end content text
	input := `{"type":"session","id":"sess1"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Fallback text"}]}}`

	scanner := bufio.NewScanner(strings.NewReader(input))
	text, err := parsePiJSONL(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Fallback text" {
		t.Errorf("got %q, want %q", text, "Fallback text")
	}
}

func TestParsePiJSONL_noEvents(t *testing.T) {
	input := "Some non-JSON noise\nAnother line\n"

	scanner := bufio.NewScanner(strings.NewReader(input))
	_, err := parsePiJSONL(scanner)
	if err == nil {
		t.Error("expected error for no JSON events")
	}
}

func TestParsePiJSONL_emptyAssistant(t *testing.T) {
	// JSON events but no assistant text
	input := `{"type":"session","id":"sess1"}
{"type":"agent_start"}`

	scanner := bufio.NewScanner(strings.NewReader(input))
	_, err := parsePiJSONL(scanner)
	if err == nil {
		t.Error("expected error for no assistant text")
	}
}
