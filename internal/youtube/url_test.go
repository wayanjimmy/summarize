package youtube

import "testing"

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc123", true},
		{"https://youtube.com/watch?v=abc123", true},
		{"https://m.youtube.com/watch?v=abc123", true},
		{"https://youtu.be/abc123", true},
		{"https://www.youtube-nocookie.com/watch?v=abc123", true},
		{"https://www.youtube.com/shorts/abc123", true},
		{"https://example.com", false},
		{"not a url", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsValidURL(tt.url)
			if got != tt.want {
				t.Errorf("IsValidURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{"https://www.youtube.com/watch?v=abc123", "abc123", false},
		{"https://youtu.be/xyz789", "xyz789", false},
		{"https://www.youtube.com/shorts/test123", "test123", false},
		{"https://www.youtube.com/embed/embed123", "embed123", false},
		{"https://example.com", "", true},
		{"https://www.youtube.com/playlist?list=PLxxx", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := ExtractVideoID(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractVideoID(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractVideoID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
