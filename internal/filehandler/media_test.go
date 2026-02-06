package filehandler

import (
	"testing"
)

func TestIsImage(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{".jpg", true},
		{".jpeg", true},
		{".JPG", true},
		{".JPEG", true},
		{".png", true},
		{".PNG", true},
		{".gif", true},
		{".webp", true},
		{".heic", true},
		{".HEIC", true},
		{".heif", true},
		{".mp4", false},
		{".mov", false},
		{".txt", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := IsImage(tt.ext)
			if result != tt.expected {
				t.Errorf("IsImage(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

func TestIsVideo(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{".mp4", true},
		{".MP4", true},
		{".mov", true},
		{".MOV", true},
		{".avi", true},
		{".webm", true},
		{".mkv", true},
		{".jpg", false},
		{".png", false},
		{".txt", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := IsVideo(tt.ext)
			if result != tt.expected {
				t.Errorf("IsVideo(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

func TestIsSupported(t *testing.T) {
	tests := []struct {
		ext      string
		expected bool
	}{
		{".jpg", true},
		{".mp4", true},
		{".heic", true},
		{".mov", true},
		{".txt", false},
		{".pdf", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			result := IsSupported(tt.ext)
			if result != tt.expected {
				t.Errorf("IsSupported(%q) = %v, want %v", tt.ext, result, tt.expected)
			}
		})
	}
}

func TestGetMIMEType(t *testing.T) {
	tests := []struct {
		ext          string
		expectedMIME string
		expectError  bool
	}{
		{".jpg", "image/jpeg", false},
		{".jpeg", "image/jpeg", false},
		{".png", "image/png", false},
		{".gif", "image/gif", false},
		{".webp", "image/webp", false},
		{".heic", "image/heic", false},
		{".heif", "image/heif", false},
		{".mp4", "video/mp4", false},
		{".mov", "video/quicktime", false},
		{".avi", "video/x-msvideo", false},
		{".webm", "video/webm", false},
		{".mkv", "video/x-matroska", false},
		{".txt", "", true},
		{".pdf", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			mime, err := GetMIMEType(tt.ext)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error for %q, got nil", tt.ext)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for %q: %v", tt.ext, err)
				}
				if mime != tt.expectedMIME {
					t.Errorf("GetMIMEType(%q) = %q, want %q", tt.ext, mime, tt.expectedMIME)
				}
			}
		})
	}
}

func TestCoordinatesToDMS(t *testing.T) {
	tests := []struct {
		name     string
		lat      float64
		lon      float64
		expected string
	}{
		{
			name:     "New York",
			lat:      40.7128,
			lon:      -74.0060,
			expected: "40°42'46.08\"N, 74°0'21.60\"W",
		},
		{
			name:     "Sydney (southern hemisphere)",
			lat:      -33.8688,
			lon:      151.2093,
			expected: "33°52'7.68\"S, 151°12'33.48\"E",
		},
		{
			name:     "Origin",
			lat:      0,
			lon:      0,
			expected: "0°0'0.00\"N, 0°0'0.00\"E",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CoordinatesToDMS(tt.lat, tt.lon)
			if result != tt.expected {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

