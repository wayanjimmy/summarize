package prompt

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	result := Build("Summarize this.", "Some content here.")
	if !strings.Contains(result, "Summarize this.") {
		t.Error("missing instructions")
	}
	if !strings.Contains(result, "Some content here.") {
		t.Error("missing content")
	}
	if !strings.Contains(result, "Source type: text") {
		t.Error("missing source type")
	}
}

func TestBuildYouTube(t *testing.T) {
	result := BuildYouTube("Summarize this.", "Test Video", "https://youtube.com/watch?v=abc", "Transcript text.")
	if !strings.Contains(result, "Summarize this.") {
		t.Error("missing instructions")
	}
	if !strings.Contains(result, "Test Video") {
		t.Error("missing title")
	}
	if !strings.Contains(result, "https://youtube.com/watch?v=abc") {
		t.Error("missing URL")
	}
	if !strings.Contains(result, "Transcript text.") {
		t.Error("missing transcript")
	}
	if !strings.Contains(result, "Source type: YouTube transcript") {
		t.Error("missing source type")
	}
}

func TestTruncate(t *testing.T) {
	// Under limit
	content, truncated := Truncate("short", 100)
	if truncated {
		t.Error("short content should not be truncated")
	}
	if content != "short" {
		t.Errorf("short content should be unchanged, got %q", content)
	}

	// Over limit
	long := strings.Repeat("a", 200)
	content, truncated = Truncate(long, 100)
	if !truncated {
		t.Error("long content should be truncated")
	}
	if len(content) <= 100 {
		t.Error("truncated content with note should exceed max chars")
	}
	if !strings.Contains(content, "[Note:") {
		t.Error("truncated content should contain note")
	}
}
