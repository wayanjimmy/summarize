package prompt

// chaptersShape returns the shape instructions for the "chapters" format.
func chaptersShape() string {
	return `Produce timestamped chapter markers from this content.

Rules:
- Group content by topic shifts into 5–15 chapters
- Each chapter MUST start with a timestamp (MM:SS or H:MM:SS)
- Format: "MM:SS Chapter Title — brief description of what's covered"
- The first chapter should cover the introduction/opening
- The final chapter should cover conclusions or closing remarks
- Chapter titles should be descriptive (3–8 words)
- Do not invent timestamps; use ones from the source content

Example:
00:00 Introduction — host introduces the problem of unreliable AI outputs
03:45 Background — overview of existing approaches and their limitations
12:30 Methodology — how they built their evaluation framework`
}
