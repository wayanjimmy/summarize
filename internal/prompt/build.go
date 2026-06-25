package prompt

// BuildForFormat composes the full prompt based on format and source type.
//   - format: one of the format constants ("summary", "chapters", etc.)
//   - refinement: user's optional custom instructions (appended after template)
//   - inputType: "youtube" or "text"
//   - title, url: metadata (may be empty for text input)
//   - content: the transcript or text to summarize
func BuildForFormat(format, refinement, inputType, title, url, content string) string {
	// Select shape instructions based on format
	var shape string
	switch format {
	case "chapters":
		shape = chaptersShape()
	case "thread":
		shape = threadShape()
	case "blog":
		shape = blogShape()
	default:
		shape = summaryShape()
	}

	// Compose: shape + grounding rules + optional refinement + source block
	full := shape + "\n\n" + groundingRules()

	if refinement != "" {
		full += "\n\nAdditional instructions: " + refinement
	}

	full += "\n\n" + sourceBlock(inputType, title, url, content)

	return full
}

// SelectContentForFormat returns the appropriate transcript content based on format.
// Chapters format uses timestamped transcripts; all others use plain text.
func SelectContentForFormat(format, transcript, transcriptWithTimestamps, inputText string) string {
	if format == "chapters" && transcriptWithTimestamps != "" {
		return transcriptWithTimestamps
	}
	if transcript != "" {
		return transcript
	}
	return inputText
}
