package youtube

import (
	"strings"
	"testing"
)

func TestParseVTT(t *testing.T) {
	tests := []struct {
		name string
		vtt  string
		want string
	}{
		{
			name: "basic VTT",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:05.000
Hello world

00:00:06.000 --> 00:00:10.000
This is a test`,
			want: "Hello world This is a test",
		},
		{
			name: "auto-captions with duplicates",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:02.000
Hello

00:00:02.000 --> 00:00:03.000
Hello

00:00:03.000 --> 00:00:04.000
Hello world`,
			want: "Hello Hello world",
		},
		{
			name: "with tags",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:05.000
<c>Tagged content</c>

00:00:06.000 --> 00:00:10.000
<c.colorCCCCCC>Colored text</c>`,
			want: "Tagged content Colored text",
		},
		{
			name: "with HTML entities",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:05.000
A &amp; B`,
			want: "A & B",
		},
		{
			name: "empty VTT",
			vtt:  `WEBVTT`,
			want: "",
		},
		{
			name: "with NOTE blocks",
			vtt: `WEBVTT

NOTE This is a comment

00:00:01.000 --> 00:00:05.000
Actual content`,
			want: "Actual content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseVTT(tt.vtt)
			gotTrimmed := strings.TrimSpace(got)
			wantTrimmed := strings.TrimSpace(tt.want)
			if gotTrimmed != wantTrimmed {
				t.Errorf("ParseVTT() = %q, want %q", gotTrimmed, wantTrimmed)
			}
		})
	}
}

func TestParseVTTWithTimestamps(t *testing.T) {
	tests := []struct {
		name string
		vtt  string
		want string
	}{
		{
			name: "basic VTT with timestamps",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:05.000
Hello world

00:00:06.000 --> 00:00:10.000
This is a test`,
			want: "00:01 Hello world\n00:06 This is a test",
		},
		{
			name: "VTT with hour prefix",
			vtt: `WEBVTT

01:02:03.000 --> 01:02:08.000
Content at one hour`,
			want: "1:02:03 Content at one hour",
		},
		{
			name: "with HTML tags",
			vtt: `WEBVTT

00:00:05.000 --> 00:00:10.000
<c>Tagged content</c>`,
			want: "00:05 Tagged content",
		},
		{
			name: "with HTML entities",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:05.000
A &amp; B`,
			want: "00:01 A & B",
		},
		{
			name: "deduplicate consecutive identical lines",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:02.000
Hello

00:00:01.000 --> 00:00:02.000
Hello`,
			want: "00:01 Hello",
		},
		{
			name: "different timestamps with same text are preserved",
			vtt: `WEBVTT

00:00:01.000 --> 00:00:02.000
Hello

00:00:02.000 --> 00:00:03.000
Hello

00:00:03.000 --> 00:00:04.000
Hello world`,
			want: "00:01 Hello\n00:02 Hello\n00:03 Hello world",
		},
		{
			name: "empty VTT",
			vtt:  `WEBVTT`,
			want: "",
		},
		{
			name: "with NOTE blocks",
			vtt: `WEBVTT

NOTE This is a comment

00:00:01.000 --> 00:00:05.000
Actual content`,
			want: "00:01 Actual content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseVTTWithTimestamps(tt.vtt)
			gotTrimmed := strings.TrimSpace(got)
			wantTrimmed := strings.TrimSpace(tt.want)
			if gotTrimmed != wantTrimmed {
				t.Errorf("ParseVTTWithTimestamps() = %q, want %q", gotTrimmed, wantTrimmed)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"00:00:01.000", "00:01"},
		{"00:05:30.500", "05:30"},
		{"01:02:03.000", "1:02:03"},
		{"12:34:56.789", "12:34:56"},
		{"00:01.000", "00:01"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := formatTimestamp(tt.input)
			if got != tt.want {
				t.Errorf("formatTimestamp(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
