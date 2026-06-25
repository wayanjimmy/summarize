package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"


)

const (
	ErrTranscriptUnavailable = "transcript_unavailable"
	ErrYouTubeIPBlocked      = "youtube_ip_blocked"
	ErrYouTubeRequestBlocked = "youtube_request_blocked"
	ErrYouTubePOTokenNeeded  = "youtube_po_token_required"
	ErrYouTubeAgeRestricted  = "youtube_age_restricted"
	ErrYouTubeUnavailable    = "youtube_video_unavailable"
	ErrYouTubeParseFailed    = "youtube_parse_failed"
)

const (
	defaultYouTubeBaseURL = "https://www.youtube.com"
	innerTubeClientName   = "ANDROID"
	innerTubeClientVer    = "20.10.38"
)

var (
	innerTubeAPIKeyRegex = regexp.MustCompile(`"INNERTUBE_API_KEY"\s*:\s*"([a-zA-Z0-9_-]+)"`)
	consentTokenRegex    = regexp.MustCompile(`name="v"\s+value="([^"]+)"`)
)

// TranscriptError is a coded transcript-fetching error.
type TranscriptError struct {
	Code    string
	Message string
	Cause   error
}

func (e *TranscriptError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func (e *TranscriptError) Unwrap() error { return e.Cause }

func transcriptError(code, message string, cause error) error {
	return &TranscriptError{Code: code, Message: message, Cause: cause}
}

func hasTranscriptErrorCode(err error, code string) bool {
	var transcriptErr *TranscriptError
	return errors.As(err, &transcriptErr) && transcriptErr.Code == code
}

func shouldRetrySourceTranscript(err error) bool {
	return hasTranscriptErrorCode(err, ErrYouTubeIPBlocked) || hasTranscriptErrorCode(err, ErrYouTubeRequestBlocked)
}

func newYouTubeHTTPClient(timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: timeout, Jar: jar}
}

type innerTubeConfig struct {
	Timeout         time.Duration
	TranscriptLangs string
	HTTPClient      *http.Client
	BaseURL         string
}

type playerResponse struct {
	PlayabilityStatus playabilityStatus `json:"playabilityStatus"`
	VideoDetails      struct {
		VideoID string `json:"videoId"`
		Title   string `json:"title"`
	} `json:"videoDetails"`
	Captions struct {
		Renderer captionTracklist `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}

type playabilityStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type captionTracklist struct {
	CaptionTracks        []captionTrack        `json:"captionTracks"`
	TranslationLanguages []translationLanguage `json:"translationLanguages"`
}

type captionTrack struct {
	BaseURL        string `json:"baseUrl"`
	LanguageCode   string `json:"languageCode"`
	Kind           string `json:"kind"`
	IsTranslatable bool   `json:"isTranslatable"`
	Name           struct {
		SimpleText string `json:"simpleText"`
		Runs       []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"name"`
}

func (t captionTrack) languageName() string {
	if t.Name.SimpleText != "" {
		return t.Name.SimpleText
	}
	if len(t.Name.Runs) > 0 {
		return t.Name.Runs[0].Text
	}
	return t.LanguageCode
}

func (t captionTrack) generated() bool { return t.Kind == "asr" }

type translationLanguage struct {
	LanguageCode string `json:"languageCode"`
	LanguageName struct {
		SimpleText string `json:"simpleText"`
		Runs       []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"languageName"`
}

type selectedTrack struct {
	track          captionTrack
	targetLanguage string
	translated     bool
}

type transcriptSegment struct {
	Start    float64
	Duration float64
	Text     string
}

// fetchTranscriptInnerTube fetches transcripts with YouTube's InnerTube and timedtext endpoints.
func fetchTranscriptInnerTube(ctx context.Context, cfg innerTubeConfig, videoURL string) (*TranscriptResult, error) {
	videoID, err := ExtractVideoID(videoURL)
	if err != nil {
		return nil, err
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.TranscriptLangs == "" {
		cfg.TranscriptLangs = "en.*,en"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultYouTubeBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = newYouTubeHTTPClient(cfg.Timeout)
	}

	apiKey, err := fetchInnerTubeAPIKey(ctx, cfg, videoID)
	if err != nil {
		return nil, err
	}

	player, err := fetchPlayerResponse(ctx, cfg, apiKey, videoID)
	if err != nil {
		return nil, err
	}
	if err := checkPlayability(player.PlayabilityStatus); err != nil {
		return nil, err
	}
	if len(player.Captions.Renderer.CaptionTracks) == 0 {
		return nil, transcriptError(ErrTranscriptUnavailable, "no caption tracks", nil)
	}

	selected, err := selectCaptionTrack(player.Captions.Renderer, cfg.TranscriptLangs)
	if err != nil {
		return nil, err
	}

	segments, err := fetchTimedText(ctx, cfg.HTTPClient, selected.track.BaseURL, selected.targetLanguage)
	if err != nil && selected.translated && shouldRetrySourceTranscript(err) {
		slog.Info("falling back to source transcript after translated transcript failed",
			"error", err,
			"source_lang", selected.track.LanguageCode,
			"target_lang", selected.targetLanguage,
		)
		segments, err = fetchTimedText(ctx, cfg.HTTPClient, selected.track.BaseURL, "")
		if err == nil {
			selected.targetLanguage = ""
			selected.translated = false
		}
	}
	if err != nil {
		return nil, err
	}
	segments = dedupeConsecutiveSegments(segments)
	if len(segments) == 0 {
		return nil, transcriptError(ErrTranscriptUnavailable, "empty transcript", nil)
	}

	videoTitle := player.VideoDetails.Title
	if videoTitle == "" {
		videoTitle = videoID
	}

	return &TranscriptResult{
		VideoID:                  videoID,
		Title:                    videoTitle,
		Transcript:               segmentsToPlainText(segments),
		TranscriptWithTimestamps: segmentsToTimestampedText(segments),
		Provider:                 "innertube",
		SourceLanguage:           selected.track.LanguageCode,
		TargetLanguage:           selected.targetLanguage,
		Translated:               selected.translated,
		Generated:                selected.track.generated(),
	}, nil
}

func fetchInnerTubeAPIKey(ctx context.Context, cfg innerTubeConfig, videoID string) (string, error) {
	watchURL := strings.TrimRight(cfg.BaseURL, "/") + "/watch?v=" + url.QueryEscape(videoID)
	body, err := getText(ctx, cfg.HTTPClient, watchURL)
	if err != nil {
		return "", err
	}

	if looksBlocked(body) {
		return "", transcriptError(ErrYouTubeRequestBlocked, "watch page blocked", nil)
	}
	if strings.Contains(body, `action="https://consent.youtube.com/s"`) || strings.Contains(body, "consent.youtube.com/s") {
		setConsentCookie(cfg.HTTPClient, cfg.BaseURL, body)
		body, err = getText(ctx, cfg.HTTPClient, watchURL)
		if err != nil {
			return "", err
		}
	}

	matches := innerTubeAPIKeyRegex.FindStringSubmatch(body)
	if len(matches) != 2 {
		return "", transcriptError(ErrYouTubeParseFailed, "INNERTUBE_API_KEY not found", nil)
	}
	return matches[1], nil
}

func getText(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", transcriptError(ErrYouTubeIPBlocked, "HTTP 429", nil)
	}
	if resp.StatusCode >= 500 {
		return "", transcriptError(ErrYouTubeRequestBlocked, resp.Status, nil)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func setConsentCookie(client *http.Client, baseURL, body string) {
	if client.Jar == nil {
		return
	}
	token := "cb"
	if matches := consentTokenRegex.FindStringSubmatch(body); len(matches) == 2 {
		token = matches[1]
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return
	}
	client.Jar.SetCookies(u, []*http.Cookie{{Name: "CONSENT", Value: "YES+" + token}})
}

func looksBlocked(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, `class="g-recaptcha"`) || strings.Contains(lower, "unusual traffic")
}

func fetchPlayerResponse(ctx context.Context, cfg innerTubeConfig, apiKey, videoID string) (*playerResponse, error) {
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/youtubei/v1/player?key=" + url.QueryEscape(apiKey)
	body := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    innerTubeClientName,
				"clientVersion": innerTubeClientVer,
			},
		},
		"videoId": videoID,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, transcriptError(ErrYouTubeIPBlocked, "HTTP 429", nil)
	}
	if resp.StatusCode >= 500 {
		return nil, transcriptError(ErrYouTubeRequestBlocked, resp.Status, nil)
	}

	var player playerResponse
	if err := json.NewDecoder(resp.Body).Decode(&player); err != nil {
		return nil, transcriptError(ErrYouTubeParseFailed, "decode player response", err)
	}
	return &player, nil
}

func checkPlayability(status playabilityStatus) error {
	if status.Status == "" || status.Status == "OK" {
		return nil
	}
	reason := strings.ToLower(status.Reason)
	switch {
	case status.Status == "LOGIN_REQUIRED" && strings.Contains(reason, "bot"):
		return transcriptError(ErrYouTubeRequestBlocked, status.Reason, nil)
	case status.Status == "LOGIN_REQUIRED" && strings.Contains(reason, "inappropriate"):
		return transcriptError(ErrYouTubeAgeRestricted, status.Reason, nil)
	case status.Status == "ERROR" || strings.Contains(reason, "unavailable"):
		return transcriptError(ErrYouTubeUnavailable, status.Reason, nil)
	default:
		return transcriptError(ErrYouTubeRequestBlocked, status.Reason, nil)
	}
}

func selectCaptionTrack(tracklist captionTracklist, transcriptLangs string) (selectedTrack, error) {
	preferences := parseLanguagePreferences(transcriptLangs)
	if len(preferences) == 0 {
		preferences = []string{"en"}
	}

	for _, preferGenerated := range []bool{false, true} {
		for _, pref := range preferences {
			for _, track := range tracklist.CaptionTracks {
				if track.generated() == preferGenerated && languageMatches(track.LanguageCode, pref) {
					return selectedTrack{track: track, targetLanguage: baseLanguage(pref)}, nil
				}
			}
		}
	}

	targets := translationTargets(preferences)
	for _, target := range targets {
		if !translationAvailable(tracklist.TranslationLanguages, target) {
			continue
		}
		for _, preferGenerated := range []bool{false, true} {
			for _, track := range tracklist.CaptionTracks {
				if track.generated() == preferGenerated && track.IsTranslatable {
					return selectedTrack{track: track, targetLanguage: target, translated: true}, nil
				}
			}
		}
	}

	return selectedTrack{}, transcriptError(ErrTranscriptUnavailable, "no matching transcript", nil)
}

func parseLanguagePreferences(raw string) []string {
	parts := strings.Split(raw, ",")
	prefs := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			prefs = append(prefs, part)
		}
	}
	return prefs
}

func languageMatches(lang, pref string) bool {
	lang = strings.ToLower(lang)
	pref = strings.ToLower(pref)
	if strings.HasSuffix(pref, ".*") {
		base := strings.TrimSuffix(pref, ".*")
		return lang == base || strings.HasPrefix(lang, base+"-")
	}
	return lang == pref
}

func baseLanguage(pref string) string {
	pref = strings.TrimSuffix(pref, ".*")
	if idx := strings.Index(pref, "-"); idx >= 0 {
		return pref[:idx]
	}
	return pref
}

func translationTargets(preferences []string) []string {
	targets := make([]string, 0, len(preferences))
	for _, pref := range preferences {
		target := baseLanguage(pref)
		if target != "" && !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	return targets
}

func translationAvailable(languages []translationLanguage, target string) bool {
	for _, lang := range languages {
		if strings.EqualFold(lang.LanguageCode, target) {
			return true
		}
	}
	return false
}

func fetchTimedText(ctx context.Context, client *http.Client, rawURL, targetLanguage string) ([]transcriptSegment, error) {
	timedTextURL, err := buildTimedTextURL(rawURL, targetLanguage)
	if err != nil {
		return nil, err
	}
	body, err := getText(ctx, client, timedTextURL)
	if err != nil {
		return nil, err
	}
	return parseTimedTextXML(body)
}

func buildTimedTextURL(rawURL, targetLanguage string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if q.Get("exp") == "xpe" || strings.Contains(rawURL, "&exp=xpe") {
		return "", transcriptError(ErrYouTubePOTokenNeeded, "timedtext URL requires PO token", nil)
	}
	q.Del("fmt")
	if targetLanguage != "" && !languageMatches(q.Get("lang"), targetLanguage) {
		q.Set("tlang", targetLanguage)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type timedTextTranscript struct {
	Texts []timedTextElement `xml:"text"`
}

type timedTextElement struct {
	Start string `xml:"start,attr"`
	Dur   string `xml:"dur,attr"`
	Text  string `xml:",innerxml"`
}

func parseTimedTextXML(data string) ([]transcriptSegment, error) {
	var transcript timedTextTranscript
	if err := xml.Unmarshal([]byte(data), &transcript); err != nil {
		return nil, transcriptError(ErrYouTubeParseFailed, "parse timedtext XML", err)
	}

	segments := make([]transcriptSegment, 0, len(transcript.Texts))
	for _, text := range transcript.Texts {
		clean := cleanTranscriptText(text.Text)
		if clean == "" {
			continue
		}
		start, err := strconv.ParseFloat(text.Start, 64)
		if err != nil {
			continue
		}
		duration := 0.0
		if text.Dur != "" {
			duration, _ = strconv.ParseFloat(text.Dur, 64)
		}
		segments = append(segments, transcriptSegment{Start: start, Duration: duration, Text: clean})
	}
	return segments, nil
}

func cleanTranscriptText(text string) string {
	text = html.UnescapeString(text)
	text = html.UnescapeString(text)
	text = vttTagRegex.ReplaceAllString(text, "")
	return strings.Join(strings.Fields(text), " ")
}

func dedupeConsecutiveSegments(segments []transcriptSegment) []transcriptSegment {
	return slices.CompactFunc(segments, func(a, b transcriptSegment) bool {
		return a.Text == b.Text
	})
}

func segmentsToPlainText(segments []transcriptSegment) string {
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Text != "" {
			lines = append(lines, segment.Text)
		}
	}
	return strings.Join(lines, " ")
}

func segmentsToTimestampedText(segments []transcriptSegment) string {
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment.Text != "" {
			lines = append(lines, formatSeconds(segment.Start)+" "+segment.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func formatSeconds(seconds float64) string {
	total := int(seconds)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
