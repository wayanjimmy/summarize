// Package youtube handles YouTube URL detection and validation.
package youtube

import (
	"fmt"
	"net/url"
	"strings"
)

// IsValidURL checks if the URL is a valid YouTube video URL.
func IsValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return IsYouTubeHost(u.Host)
}

// IsYouTubeHost checks if the host is a known YouTube host.
func IsYouTubeHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be", "www.youtube-nocookie.com":
		return true
	}
	return false
}

// ExtractVideoID extracts the YouTube video ID from a URL.
func ExtractVideoID(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}

	host := strings.ToLower(u.Host)

	// youtu.be short URLs: video ID is the path
	if host == "youtu.be" {
		id := strings.TrimPrefix(u.Path, "/")
		if id == "" {
			return "", fmt.Errorf("no video ID in youtu.be URL")
		}
		return id, nil
	}

	// youtube.com/watch?v=...
	if u.Query().Get("v") != "" {
		return u.Query().Get("v"), nil
	}

	// youtube.com/shorts/VIDEO_ID
	if strings.HasPrefix(u.Path, "/shorts/") {
		id := strings.TrimPrefix(u.Path, "/shorts/")
		id = strings.Split(id, "/")[0]
		if id != "" {
			return id, nil
		}
	}

	// youtube.com/embed/VIDEO_ID
	if strings.HasPrefix(u.Path, "/embed/") {
		id := strings.TrimPrefix(u.Path, "/embed/")
		id = strings.Split(id, "/")[0]
		if id != "" {
			return id, nil
		}
	}

	return "", fmt.Errorf("no video ID found in URL: %s", rawURL)
}
