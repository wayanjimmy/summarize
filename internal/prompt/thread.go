package prompt

// threadShape returns the shape instructions for the "thread" format.
func threadShape() string {
	return `Produce a Twitter/X thread from this content.

Rules:
- Output the thread directly — start immediately with "1/", no preamble, intro, or commentary
- Do NOT write anything before the first post or after the last post
- Each post MUST be self-contained (readable without other posts)
- Maximum 280 characters per post (including the "N/" prefix)
- Number each post sequentially: "1/", "2/", "3/"
- Aim for 8–12 posts for short content, up to 20 for long content
- First post: hook the reader with the most surprising/interesting finding
- Final post: key takeaway or call to action
- Do not fabricate claims not in the source

Example:
1/ We studied 10,000 AI outputs and found something surprising about prompt length. 🧵

2/ Shorter prompts actually produced HIGHER quality summaries — but only for videos under 10 minutes.`
}
