package youtube

import (
	"html"
	"regexp"
	"slices"
	"strings"
)

// vttTagRegex matches HTML-like tags in VTT content (e.g. <c>, </c>, <c.colorCCCCCC>, <00:01:02.000>)
var vttTagRegex = regexp.MustCompile(`<[^>]+>`)

// vttTimestampRegex matches VTT cue timestamp lines
var vttTimestampRegex = regexp.MustCompile(`\d{2}:\d{2}[:\.\d]+\s*-->\s*\d{2}:\d{2}`)

// vttTimestampLineRegex matches and captures start time from a VTT timestamp line.
// Captures the full HH:MM:SS.mmm start timestamp.
var vttTimestampLineRegex = regexp.MustCompile(`^(\d{2}:\d{2}:\d{2}\.\d{3})\s*-->\s*\d{2}:\d{2}`)

// ParseVTT parses a WebVTT format string into plain text.
func ParseVTT(data string) string {
	lines := strings.Split(data, "\n")
	var result []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Skip WEBVTT header
		if strings.HasPrefix(line, "WEBVTT") {
			continue
		}

		// Skip timestamp/cue lines
		if vttTimestampRegex.MatchString(line) {
			continue
		}

		// Skip NOTE, STYLE, REGION blocks
		if strings.HasPrefix(line, "NOTE") || strings.HasPrefix(line, "STYLE") || strings.HasPrefix(line, "REGION") {
			continue
		}

		// Skip standalone numbers (cue identifiers)
		if isCueIdentifier(line) {
			continue
		}

		// Strip VTT tags
		line = vttTagRegex.ReplaceAllString(line, "")

		// Unescape HTML entities
		line = html.UnescapeString(line)

		// Clean whitespace
		line = strings.Join(strings.Fields(line), " ")

		if line != "" {
			result = append(result, line)
		}
	}

	// Deduplicate consecutive identical lines
	result = slices.Compact(result)

	return strings.Join(result, " ")
}

// ParseVTTWithTimestamps parses WebVTT into timestamped text.
// Output format: "MM:SS text of that segment\n..."
func ParseVTTWithTimestamps(data string) string {
	lines := strings.Split(data, "\n")
	var result []string
	var currentTS string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Skip WEBVTT header
		if strings.HasPrefix(line, "WEBVTT") {
			continue
		}

		// Match timestamp line -> extract start time
		if m := vttTimestampLineRegex.FindStringSubmatch(line); m != nil {
			currentTS = formatTimestamp(m[1])
			continue
		}

		// Skip NOTE, STYLE, REGION blocks
		if strings.HasPrefix(line, "NOTE") || strings.HasPrefix(line, "STYLE") || strings.HasPrefix(line, "REGION") {
			continue
		}

		// Skip standalone numbers (cue identifiers)
		if isCueIdentifier(line) {
			continue
		}

		// Strip VTT tags
		line = vttTagRegex.ReplaceAllString(line, "")

		// Unescape HTML entities
		line = html.UnescapeString(line)

		// Clean whitespace
		line = strings.Join(strings.Fields(line), " ")

		if line == "" {
			continue
		}

		// Prepend timestamp if available
		if currentTS != "" {
			result = append(result, currentTS+" "+line)
		} else {
			result = append(result, line)
		}
	}

	// Deduplicate consecutive identical lines
	result = slices.Compact(result)

	return strings.Join(result, "\n")
}

// formatTimestamp converts a VTT timestamp (HH:MM:SS.mmm or MM:SS.mmm) to MM:SS or H:MM:SS.
func formatTimestamp(ts string) string {
	// Strip milliseconds
	if idx := strings.LastIndex(ts, "."); idx >= 0 {
		ts = ts[:idx]
	}

	parts := strings.Split(ts, ":")
	switch len(parts) {
	case 3:
		// HH:MM:SS
		h := parts[0]
		m := parts[1]
		s := parts[2]
		if h == "00" {
			return m + ":" + s
		}
		// Remove leading zero from hour
		h = strings.TrimLeft(h, "0")
		if h == "" {
			h = "0"
		}
		return h + ":" + m + ":" + s
	case 2:
		// MM:SS
		return ts
	default:
		return ts
	}
}

// isCueIdentifier returns true if the line looks like an auto-generated cue ID (just digits).
func isCueIdentifier(line string) bool {
	for _, r := range line {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(line) > 0
}
