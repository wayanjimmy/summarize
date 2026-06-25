package prompt

// summaryShape returns the shape instructions for the "summary" format.
// This mirrors the existing DefaultPrompt behavior for backward compatibility.
func summaryShape() string {
	return `Summarize the following content clearly and accurately.
Include:
- A concise overview
- Key points
- Important details, decisions, or claims
- Action items if any`
}
