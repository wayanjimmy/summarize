// Package prompt builds LLM prompts for summarization.
package prompt

import "fmt"

// Build constructs the full prompt for a text input.
func Build(instructions, text string) string {
	return fmt.Sprintf(`%s

Source type: text

Content:
%s`, instructions, text)
}

// BuildYouTube constructs the full prompt for a YouTube transcript.
func BuildYouTube(instructions, title, url, transcript string) string {
	return fmt.Sprintf(`%s

Source type: YouTube transcript
Title: %s
URL: %s

Content:
%s`, instructions, title, url, transcript)
}

// Truncate truncates content to maxChars and prepends a note if truncated.
func Truncate(content string, maxChars int) (string, bool) {
	if len(content) <= maxChars {
		return content, false
	}
	truncated := content[:maxChars]
	note := "[Note: source content was truncated to " + fmt.Sprintf("%d", maxChars) + " characters for this v1 summarizer.]\n\n"
	return note + truncated, true
}
