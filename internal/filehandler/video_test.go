package filehandler

import (
	"strings"
	"testing"
	"time"
)

func TestParseISO6709Location(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectedLat float64
		expectedLon float64
	}{
		{
			name:        "Basic format with trailing slash",
			input:       "+38.0048-084.4848/",
			expectedLat: 38.0048,
			expectedLon: -84.4848,
		},
		{
			name:        "Format with altitude",
			input:       "+37.7749-122.4194+000.000/",
			expectedLat: 37.7749,
			expectedLon: -122.4194,
		},
		{
			name:        "New York coordinates",
			input:       "+40.7128-074.0060/",
			expectedLat: 40.7128,
			expectedLon: -74.0060,
		},
		{
			name:        "Southern hemisphere",
			input:       "-33.8688+151.2093/",
			expectedLat: -33.8688,
			expectedLon: 151.2093,
		},
		{
			name:        "Without trailing slash",
			input:       "+51.5074-000.1278",
			expectedLat: 51.5074,
			expectedLon: -0.1278,
		},
		{
			name:        "Empty string",
			input:       "",
			expectedLat: 0,
			expectedLon: 0,
		},
		{
			name:        "Invalid format",
			input:       "invalid",
			expectedLat: 0,
			expectedLon: 0,
		},
		{
			name:        "Tokyo with altitude",
			input:       "+35.6762+139.6503+040.000/",
			expectedLat: 35.6762,
			expectedLon: 139.6503,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lat, lon := parseISO6709Location(tt.input)
			if !floatEquals(lat, tt.expectedLat, 0.0001) {
				t.Errorf("latitude: got %v, want %v", lat, tt.expectedLat)
			}
			if !floatEquals(lon, tt.expectedLon, 0.0001) {
				t.Errorf("longitude: got %v, want %v", lon, tt.expectedLon)
			}
		})
	}
}

func TestParseFrameRate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
	}{
		{
			name:     "Standard 30 fps",
			input:    "30/1",
			expected: 30.0,
		},
		{
			name:     "Standard 60 fps",
			input:    "60/1",
			expected: 60.0,
		},
		{
			name:     "NTSC 29.97 fps",
			input:    "30000/1001",
			expected: 29.97002997,
		},
		{
			name:     "NTSC 59.94 fps",
			input:    "60000/1001",
			expected: 59.94005994,
		},
		{
			name:     "24 fps (film)",
			input:    "24/1",
			expected: 24.0,
		},
		{
			name:     "Plain number",
			input:    "25",
			expected: 25.0,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFrameRate(tt.input)
			if !floatEquals(result, tt.expected, 0.0001) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "30 seconds",
			duration: 30 * time.Second,
			expected: "0:30",
		},
		{
			name:     "2 minutes 30 seconds",
			duration: 2*time.Minute + 30*time.Second,
			expected: "2:30",
		},
		{
			name:     "1 hour 5 minutes 30 seconds",
			duration: 1*time.Hour + 5*time.Minute + 30*time.Second,
			expected: "1:05:30",
		},
		{
			name:     "0 duration",
			duration: 0,
			expected: "0:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestVideoMetadataInterface(t *testing.T) {
	meta := &VideoMetadata{
		Latitude:   37.7749,
		Longitude:  -122.4194,
		HasGPS:     true,
		CreateDate: time.Date(2024, 12, 31, 14, 0, 0, 0, time.UTC),
		HasDate:    true,
		Duration:   time.Duration(120 * time.Second),
		Width:      1920,
		Height:     1080,
		FrameRate:  60.0,
		Codec:      "h264",
	}

	// Test interface compliance
	var _ MediaMetadata = meta

	if meta.GetMediaType() != "video" {
		t.Errorf("GetMediaType() = %q, want %q", meta.GetMediaType(), "video")
	}

	if !meta.HasGPSData() {
		t.Error("HasGPSData() = false, want true")
	}

	lat, lon := meta.GetGPS()
	if lat != 37.7749 || lon != -122.4194 {
		t.Errorf("GetGPS() = (%v, %v), want (37.7749, -122.4194)", lat, lon)
	}

	// Test FormatMetadataContext produces non-empty output
	context := meta.FormatMetadataContext()
	if len(context) == 0 {
		t.Error("FormatMetadataContext() returned empty string")
	}
	if !strings.Contains(context, "GPS Coordinates") {
		t.Error("FormatMetadataContext() missing GPS section")
	}
	if !strings.Contains(context, "Video Properties") {
		t.Error("FormatMetadataContext() missing Video Properties section")
	}
	if !strings.Contains(context, "Full HD") {
		t.Error("FormatMetadataContext() missing resolution label")
	}
}

func TestVideoMetadataNoGPS(t *testing.T) {
	meta := &VideoMetadata{
		HasGPS:  false,
		HasDate: false,
	}

	context := meta.FormatMetadataContext()
	if !strings.Contains(context, "Not available") {
		t.Error("FormatMetadataContext() should indicate metadata not available")
	}
}

func TestVideoMetadataGetters(t *testing.T) {
	tests := []struct {
		name     string
		meta     *VideoMetadata
		wantType string
		wantGPS  bool
		wantDate bool
		wantLat  float64
		wantLon  float64
	}{
		{
			name: "Full metadata",
			meta: &VideoMetadata{
				Latitude:   38.0048,
				Longitude:  -84.4848,
				HasGPS:     true,
				CreateDate: time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
				HasDate:    true,
			},
			wantType: "video",
			wantGPS:  true,
			wantDate: true,
			wantLat:  38.0048,
			wantLon:  -84.4848,
		},
		{
			name: "No GPS",
			meta: &VideoMetadata{
				HasGPS:     false,
				CreateDate: time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
				HasDate:    true,
			},
			wantType: "video",
			wantGPS:  false,
			wantDate: true,
			wantLat:  0,
			wantLon:  0,
		},
		{
			name: "Empty metadata",
			meta: &VideoMetadata{
				HasGPS:  false,
				HasDate: false,
			},
			wantType: "video",
			wantGPS:  false,
			wantDate: false,
			wantLat:  0,
			wantLon:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.meta.GetMediaType(); got != tt.wantType {
				t.Errorf("GetMediaType() = %v, want %v", got, tt.wantType)
			}
			if got := tt.meta.HasGPSData(); got != tt.wantGPS {
				t.Errorf("HasGPSData() = %v, want %v", got, tt.wantGPS)
			}
			if got := tt.meta.HasDateData(); got != tt.wantDate {
				t.Errorf("HasDateData() = %v, want %v", got, tt.wantDate)
			}
			lat, lon := tt.meta.GetGPS()
			if lat != tt.wantLat || lon != tt.wantLon {
				t.Errorf("GetGPS() = (%v, %v), want (%v, %v)", lat, lon, tt.wantLat, tt.wantLon)
			}
		})
	}
}

func TestVideoMetadataFormatContext(t *testing.T) {
	tests := []struct {
		name     string
		meta     *VideoMetadata
		contains []string
	}{
		{
			name: "Full HD video with GPS",
			meta: &VideoMetadata{
				Latitude:   37.7749,
				Longitude:  -122.4194,
				HasGPS:     true,
				CreateDate: time.Date(2024, 12, 31, 14, 0, 0, 0, time.UTC),
				HasDate:    true,
				Duration:   2 * time.Minute,
				Width:      1920,
				Height:     1080,
				FrameRate:  60.0,
				Codec:      "h264",
			},
			contains: []string{
				"GPS Coordinates",
				"37.774900",
				"-122.419400",
				"google.com/maps",
				"Video Properties",
				"1920x1080",
				"Full HD",
				"60.00 fps",
				"h264",
			},
		},
		{
			name: "4K video",
			meta: &VideoMetadata{
				Width:  3840,
				Height: 2160,
			},
			contains: []string{
				"3840x2160",
				"4K UHD",
			},
		},
		{
			name: "With audio",
			meta: &VideoMetadata{
				AudioCodec: "aac",
				AudioRate:  48000,
			},
			contains: []string{
				"Audio Properties",
				"aac",
				"48000 Hz",
			},
		},
		{
			name: "With device info",
			meta: &VideoMetadata{
				DeviceMake:  "Apple",
				DeviceModel: "iPhone 15 Pro",
			},
			contains: []string{
				"Recording Device",
				"Apple",
				"iPhone 15 Pro",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context := tt.meta.FormatMetadataContext()
			for _, substr := range tt.contains {
				if !strings.Contains(context, substr) {
					t.Errorf("FormatMetadataContext() missing %q", substr)
				}
			}
		})
	}
}

func TestIsFFprobeAvailable(t *testing.T) {
	// This test just verifies the function runs without panicking
	// The actual result depends on the system configuration
	_ = IsFFprobeAvailable()
}

func TestCheckFFprobeAvailable(t *testing.T) {
	// This test just verifies the function runs without panicking
	// The actual result depends on the system configuration
	err := CheckFFprobeAvailable()
	if err != nil {
		t.Logf("ffprobe not available: %v (this is OK if FFmpeg is not installed)", err)
	}
}

// Helper function for float comparison
func floatEquals(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < tolerance
}
