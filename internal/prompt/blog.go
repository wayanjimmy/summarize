package prompt

// blogShape returns the shape instructions for the "blog" format.
func blogShape() string {
	return `Produce a blog post article from this content.

Rules:
- Output the COMPLETE blog post directly as your response text
- Do NOT write to any file — the full blog content must be your response
- Include a compelling title (not just the video title)
- Structure with H2/H3 headings (## and ### in markdown)
- Include a brief intro paragraph that hooks the reader
- Group content into logical sections with descriptive headings
- End with a "Key Takeaways" section (3–5 bullet points)
- Use markdown formatting (bold, lists, emphasis) for readability
- Do not fabricate claims not in the source

Example:
# How We Cut AI Costs by 60% Without Losing Quality

## The Problem
After six months of running our summarization pipeline...

## What We Tried
### Approach 1: Smaller Models
...

## Key Takeaways
- Smaller models work for short content but fail on nuance
- Caching cuts costs more than model downsizing`
}
