package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildForFormat_Summary(t *testing.T) {
	testBuildForFormat(t, "summary", "summary", "", "youtube", "Test Video", "https://youtube.com/watch?v=abc", "Hello world this is a test transcript")
}

func TestBuildForFormat_Chapters(t *testing.T) {
	content := "00:01 Hello world\n00:06 This is a test transcript"
	testBuildForFormat(t, "chapters", "chapters", "", "youtube", "Test Video", "https://youtube.com/watch?v=abc", content)
}

func TestBuildForFormat_Thread(t *testing.T) {
	testBuildForFormat(t, "thread", "thread", "", "text", "", "", "Some text content here")
}

func TestBuildForFormat_Blog(t *testing.T) {
	testBuildForFormat(t, "blog", "blog", "", "text", "", "", "Some blog-worthy content here")
}

func TestBuildForFormat_WithRefinement(t *testing.T) {
	got := BuildForFormat("summary", "Focus on technical details only.", "youtube", "Test", "https://example.com", "Content here.")
	if got == "" {
		t.Fatal("BuildForFormat returned empty")
	}
	if !strings.Contains(got, "Focus on technical details only.") {
		t.Error("expected refinement to be included")
	}
	if !strings.Contains(got, "Additional instructions:") {
		t.Error("expected 'Additional instructions:' marker")
	}
}

func TestBuildForFormat_InvalidFormatDefaultsToSummary(t *testing.T) {
	got := BuildForFormat("invalid_format", "", "text", "", "", "content")
	if !strings.Contains(got, "Summarize the following") {
		t.Error("invalid format should default to summary shape")
	}
}

func testBuildForFormat(t *testing.T, name, format, refinement, inputType, title, url, content string) {
	t.Helper()

	got := BuildForFormat(format, refinement, inputType, title, url, content)

	// Read golden file
	goldenPath := filepath.Join("testdata", name+"_golden.txt")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		// If golden file doesn't exist, write actual output as new golden
		t.Logf("Golden file %s not found, writing actual output", goldenPath)
		os.WriteFile(goldenPath, []byte(got), 0644)
		return
	}

	if got != string(want) {
		t.Errorf("BuildForFormat(%q) output doesn't match golden file:\nexpected:\n%s\n\ngot:\n%s", name, string(want), got)
	}
}
