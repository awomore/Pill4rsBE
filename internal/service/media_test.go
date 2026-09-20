package service

import "testing"

func TestMediaKind(t *testing.T) {
	cases := []struct {
		contentType string
		filename    string
		want        string
		wantErr     bool
	}{
		{"image/jpeg", "ad.jpg", MediaKindImage, false},
		{"image/png", "ad.png", MediaKindImage, false},
		{"video/mp4", "clip.mp4", MediaKindVideo, false},
		{"video/quicktime", "clip.mov", MediaKindVideo, false},
		{"", "photo.webp", MediaKindImage, false},
		{"", "clip.webm", MediaKindVideo, false},
		{"application/pdf", "doc.pdf", "", true},
	}
	for _, tc := range cases {
		got, err := mediaKind(tc.contentType, tc.filename)
		if tc.wantErr {
			if err == nil {
				t.Errorf("mediaKind(%q, %q) expected error", tc.contentType, tc.filename)
			}
			continue
		}
		if err != nil {
			t.Errorf("mediaKind(%q, %q) unexpected error: %v", tc.contentType, tc.filename, err)
			continue
		}
		if got != tc.want {
			t.Errorf("mediaKind(%q, %q) = %q, want %q", tc.contentType, tc.filename, got, tc.want)
		}
	}
}
