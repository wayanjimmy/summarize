package prompt

import "fmt"

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
	var header string
	switch inputType {
	case "youtube":
		header = "Source type: YouTube transcript"
		if title != "" {
			header += "\nTitle: " + title
		}
		if url != "" {
			header += "\nURL: " + url
		}
	default:
		header = "Source type: text"
	}

	return fmt.Sprintf("%s\n\nContent:\n%s", header, content)
}
