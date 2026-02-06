package filehandler

import (
	"strings"
	"testing"
	"time"
)

func TestImageMetadataInterface(t *testing.T) {
	meta := &ImageMetadata{
		Latitude:    40.7128,
		Longitude:   -74.0060,
		HasGPS:      true,
		DateTaken:   time.Date(2024, 12, 31, 10, 30, 0, 0, time.UTC),
		HasDate:     true,
		CameraMake:  "Apple",
		CameraModel: "iPhone 15 Pro",
	}

	// Test interface compliance
	var _ MediaMetadata = meta

	if meta.GetMediaType() != "image" {
		t.Errorf("GetMediaType() = %q, want %q", meta.GetMediaType(), "image")
	}

	if !meta.HasGPSData() {
		t.Error("HasGPSData() = false, want true")
	}

	lat, lon := meta.GetGPS()
	if lat != 40.7128 || lon != -74.0060 {
		t.Errorf("GetGPS() = (%v, %v), want (40.7128, -74.0060)", lat, lon)
	}

	if !meta.HasDateData() {
		t.Error("HasDateData() = false, want true")
	}

	date := meta.GetDate()
	if date.Year() != 2024 || date.Month() != 12 || date.Day() != 31 {
		t.Errorf("GetDate() = %v, want 2024-12-31", date)
	}

	// Test FormatMetadataContext produces non-empty output
	context := meta.FormatMetadataContext()
	if len(context) == 0 {
		t.Error("FormatMetadataContext() returned empty string")
	}
	if !strings.Contains(context, "GPS Coordinates") {
		t.Error("FormatMetadataContext() missing GPS section")
	}
	if !strings.Contains(context, "Date/Time Taken") {
		t.Error("FormatMetadataContext() missing Date section")
	}
	if !strings.Contains(context, "Camera") {
		t.Error("FormatMetadataContext() missing Camera section")
	}
}

func TestImageMetadataNoGPS(t *testing.T) {
	meta := &ImageMetadata{
		HasGPS:  false,
		HasDate: false,
	}

	context := meta.FormatMetadataContext()
	if !strings.Contains(context, "Not available") {
		t.Error("FormatMetadataContext() should indicate metadata not available")
	}
}

func TestImageMetadataGetters(t *testing.T) {
	tests := []struct {
		name     string
		meta     *ImageMetadata
		wantType string
		wantGPS  bool
		wantDate bool
		wantLat  float64
		wantLon  float64
	}{
		{
			name: "Full metadata",
			meta: &ImageMetadata{
				Latitude:  38.0048,
				Longitude: -84.4848,
				HasGPS:    true,
				DateTaken: time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
				HasDate:   true,
			},
			wantType: "image",
			wantGPS:  true,
			wantDate: true,
			wantLat:  38.0048,
			wantLon:  -84.4848,
		},
		{
			name: "No GPS",
			meta: &ImageMetadata{
				HasGPS:    false,
				DateTaken: time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
				HasDate:   true,
			},
			wantType: "image",
			wantGPS:  false,
			wantDate: true,
			wantLat:  0,
			wantLon:  0,
		},
		{
			name: "Empty metadata",
			meta: &ImageMetadata{
				HasGPS:  false,
				HasDate: false,
			},
			wantType: "image",
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

func TestImageMetadataFormatContext(t *testing.T) {
	tests := []struct {
		name     string
		meta     *ImageMetadata
		contains []string
	}{
		{
			name: "With GPS and date",
			meta: &ImageMetadata{
				Latitude:    40.7128,
				Longitude:   -74.0060,
				HasGPS:      true,
				DateTaken:   time.Date(2024, 12, 31, 10, 30, 0, 0, time.UTC),
				HasDate:     true,
				CameraMake:  "Apple",
				CameraModel: "iPhone 15",
			},
			contains: []string{
				"GPS Coordinates",
				"40.712800",
				"-74.006000",
				"google.com/maps",
				"Date/Time Taken",
				"December 31, 2024",
				"Camera",
				"Apple",
				"iPhone 15",
			},
		},
		{
			name: "Without GPS or date",
			meta: &ImageMetadata{
				HasGPS:  false,
				HasDate: false,
			},
			contains: []string{
				"Not available in image metadata",
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
