package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchTranscriptInnerTubeTranslatesGeneratedTranscript(t *testing.T) {
	var sawTranslation bool
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/watch":
			w.Write([]byte(`{"INNERTUBE_API_KEY":"test-key"}`))
		case "/youtubei/v1/player":
			w.Header().Set("Content-Type", "application/json")
			response := `{"playabilityStatus":{"status":"OK"},"videoDetails":{"videoId":"fqART0qIJZ8","title":"Sample"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + server.URL + `/api/timedtext?v=fqART0qIJZ8&lang=id&fmt=srv3","name":{"runs":[{"text":"Indonesian"}]},"languageCode":"id","kind":"asr","isTranslatable":true}],"translationLanguages":[{"languageCode":"en","languageName":{"runs":[{"text":"English"}]}}]}}}`
			w.Write([]byte(response))
		case "/api/timedtext":
			if r.URL.Query().Get("fmt") != "" {
				t.Fatalf("fmt query should have been stripped, got %q", r.URL.RawQuery)
			}
			if got := r.URL.Query().Get("tlang"); got != "en" {
				t.Fatalf("tlang = %q, want en", got)
			}
			sawTranslation = true
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<transcript><text start="1.2" dur="2">Hello &amp;amp; &lt;i&gt;welcome&lt;/i&gt;</text><text start="3"></text></transcript>`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := fetchTranscriptInnerTube(context.Background(), innerTubeConfig{
		Timeout:         time.Second,
		TranscriptLangs: "en.*,en",
		HTTPClient:      server.Client(),
		BaseURL:         server.URL,
	}, "https://www.youtube.com/watch?v=fqART0qIJZ8")
	if err != nil {
		t.Fatalf("fetchTranscriptInnerTube() error = %v", err)
	}
	if !sawTranslation {
		t.Fatal("timedtext endpoint was not called with translation")
	}
	if !result.Translated {
		t.Fatal("Translated = false, want true")
	}
	if result.SourceLanguage != "id" || result.TargetLanguage != "en" {
		t.Fatalf("languages = %q -> %q, want id -> en", result.SourceLanguage, result.TargetLanguage)
	}
	if result.Transcript != "Hello & welcome" {
		t.Fatalf("Transcript = %q, want %q", result.Transcript, "Hello & welcome")
	}
	if result.TranscriptWithTimestamps != "00:01 Hello & welcome" {
		t.Fatalf("TranscriptWithTimestamps = %q", result.TranscriptWithTimestamps)
	}
}

func TestFetchTranscriptInnerTubeFallsBackToSourceWhenTranslationBlocked(t *testing.T) {
	var sawTranslation bool
	var sawSource bool
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/watch":
			w.Write([]byte(`{"INNERTUBE_API_KEY":"test-key"}`))
		case "/youtubei/v1/player":
			w.Header().Set("Content-Type", "application/json")
			response := `{"playabilityStatus":{"status":"OK"},"videoDetails":{"videoId":"fqART0qIJZ8","title":"Sample"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + server.URL + `/api/timedtext?v=fqART0qIJZ8&lang=id&fmt=srv3","name":{"runs":[{"text":"Indonesian"}]},"languageCode":"id","kind":"asr","isTranslatable":true}],"translationLanguages":[{"languageCode":"en","languageName":{"runs":[{"text":"English"}]}}]}}}`
			w.Write([]byte(response))
		case "/api/timedtext":
			if r.URL.Query().Get("tlang") == "en" {
				sawTranslation = true
				http.Error(w, "blocked", http.StatusTooManyRequests)
				return
			}
			sawSource = true
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<transcript><text start="1" dur="2">source text</text></transcript>`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := fetchTranscriptInnerTube(context.Background(), innerTubeConfig{
		Timeout:         time.Second,
		TranscriptLangs: "en.*,en",
		HTTPClient:      server.Client(),
		BaseURL:         server.URL,
	}, "https://www.youtube.com/watch?v=fqART0qIJZ8")
	if err != nil {
		t.Fatalf("fetchTranscriptInnerTube() error = %v", err)
	}
	if !sawTranslation || !sawSource {
		t.Fatalf("sawTranslation=%v sawSource=%v, want both true", sawTranslation, sawSource)
	}
	if result.Translated {
		t.Fatal("Translated = true, want false after source fallback")
	}
	if result.SourceLanguage != "id" || result.TargetLanguage != "" {
		t.Fatalf("languages = %q -> %q, want id -> empty", result.SourceLanguage, result.TargetLanguage)
	}
	if result.Transcript != "source text" {
		t.Fatalf("Transcript = %q, want source text", result.Transcript)
	}
}

func TestBuildTimedTextURLRequiresPOToken(t *testing.T) {
	_, err := buildTimedTextURL("https://www.youtube.com/api/timedtext?v=x&lang=id&exp=xpe", "en")
	if !hasTranscriptErrorCode(err, ErrYouTubePOTokenNeeded) {
		t.Fatalf("error = %v, want %s", err, ErrYouTubePOTokenNeeded)
	}
}

func TestParseTimedTextXML(t *testing.T) {
	segments, err := parseTimedTextXML(`<transcript>
		<text start="0" dur="1.5">A &amp;amp; B</text>
		<text start="1.5">&lt;b&gt;bold&lt;/b&gt;</text>
		<text start="2.5">before <i>inside</i> after</text>
		<text start="2"></text>
	</transcript>`)
	if err != nil {
		t.Fatalf("parseTimedTextXML() error = %v", err)
	}
	got := segmentsToPlainText(segments)
	if got != "A & B bold before inside after" {
		t.Fatalf("plain text = %q", got)
	}
	if gotTS := segmentsToTimestampedText(segments); !strings.Contains(gotTS, "00:01 bold") {
		t.Fatalf("timestamped text = %q", gotTS)
	}
}
