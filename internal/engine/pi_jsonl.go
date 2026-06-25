package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// parsePiJSONL reads pi CLI JSONL output and extracts the final assistant text.
// Priority:
//  1. message_update.assistantMessageEvent.type == "text_end" with text
//  2. accumulated text_delta deltas
//  3. message_end content text
func parsePiJSONL(stdout *bufio.Scanner) (string, error) {
	var deltaText strings.Builder
	var fullFromTextEnd string
	var messageEndText string
	var eventCount int

	for stdout.Scan() {
		line := stdout.Text()
		if line == "" {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue // skip non-JSON lines
		}
		eventCount++

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_update":
			if ame, ok := event["assistantMessageEvent"].(map[string]any); ok {
				ameType, _ := ame["type"].(string)
				switch ameType {
				case "text_delta":
					if delta, ok := ame["delta"].(string); ok {
						deltaText.WriteString(delta)
					}
				case "text_end":
					if text, ok := ame["text"].(string); ok {
						fullFromTextEnd = text
					}
				}
			}
		case "message_end":
			if msg, ok := event["message"].(map[string]any); ok {
				if contentArr, ok := msg["content"].([]any); ok {
					for _, item := range contentArr {
						if contentItem, ok := item.(map[string]any); ok {
							if ct, ok := contentItem["type"].(string); ok && ct == "text" {
								if t, ok := contentItem["text"].(string); ok && t != "" {
									messageEndText = t
								}
							}
						}
					}
				}
			}
		}
	}

	if eventCount == 0 {
		return "", fmt.Errorf("no JSON events from pi")
	}

	// Priority: text_end > accumulated deltas > message_end
	if fullFromTextEnd != "" {
		return fullFromTextEnd, nil
	}
	if deltaText.Len() > 0 {
		return deltaText.String(), nil
	}
	if messageEndText != "" {
		return messageEndText, nil
	}

	return "", fmt.Errorf("no assistant text in pi output")
}

// piStderr reads stderr from the pi process, capped at maxBytes.
func piStderr(stderr *bufio.Scanner, maxBytes int) string {
	var buf strings.Builder
	for stderr.Scan() {
		if buf.Len() < maxBytes {
			buf.WriteString(stderr.Text())
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

// piExitError extracts exit code from exec.ExitError if available.
func piExitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("pi exited with status %d", exitErr.ExitCode())
	}
	return err
}
