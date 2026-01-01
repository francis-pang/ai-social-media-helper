package filehandler

import (
	"testing"
)

func TestCheckFFmpegAvailable(t *testing.T) {
	// This test will pass if FFmpeg is installed, or gracefully report if not
	err := CheckFFmpegAvailable()
	if err != nil {
		t.Logf("FFmpeg not available (expected in some environments): %v", err)
		// Don't fail the test - FFmpeg may not be installed in CI
	} else {
		t.Log("FFmpeg is available")
	}
}

func TestIsFFmpegAvailable(t *testing.T) {
	available := IsFFmpegAvailable()
	t.Logf("FFmpeg available: %v", available)
	// Just verify it doesn't panic
}

func TestBuildFFmpegArgs_NoMetadata(t *testing.T) {
	args := buildFFmpegArgs("input.mp4", "output.webm", nil)

	// Verify essential arguments are present
	assertContains(t, args, "-c:v", "libsvtav1")
	assertContains(t, args, "-c:a", "libopus")
	assertContains(t, args, "-b:a", AudioBitrate)
	assertContains(t, args, "-ac", "1")
	assertContains(t, args, "-vbr", "on")
	assertContains(t, args, "-y", "output.webm")

	// Verify frame rate is capped at max
	assertContains(t, args, "-r", "5.00")
}

func TestBuildFFmpegArgs_WithHighFPSSource(t *testing.T) {
	// Source video at 60 FPS should be capped to 5 FPS
	metadata := &VideoMetadata{
		FrameRate: 60.0,
		Width:     3840,
		Height:    2160,
		AudioRate: 48000,
	}

	args := buildFFmpegArgs("input.mp4", "output.webm", metadata)

	// Verify frame rate is capped at MaxFrameRate (5), not 60
	assertContains(t, args, "-r", "5.00")

	// Verify audio sample rate is capped at 44100
	assertContains(t, args, "-ar", "44100")
}

func TestBuildFFmpegArgs_WithLowFPSSource(t *testing.T) {
	// Source video at 3 FPS should NOT be upscaled to 5 FPS
	metadata := &VideoMetadata{
		FrameRate: 3.0,
		Width:     640,
		Height:    480,
		AudioRate: 22050,
	}

	args := buildFFmpegArgs("input.mp4", "output.webm", metadata)

	// Verify frame rate preserves source (3 FPS), not upscaled to 5
	assertContains(t, args, "-r", "3.00")

	// Verify audio sample rate is NOT set (let FFmpeg preserve 22050)
	// Since source (22050) is less than max (44100), we don't force it
	assertNotContains(t, args, "-ar")
}

func TestBuildFFmpegArgs_VideoFilterPresent(t *testing.T) {
	args := buildFFmpegArgs("input.mp4", "output.webm", nil)

	// Verify video filter includes scale and format
	found := false
	for _, arg := range args {
		if len(arg) > 5 && arg[:5] == "scale" {
			found = true
			break
		}
	}
	// The -vf argument should contain scale
	for i, arg := range args {
		if arg == "-vf" && i+1 < len(args) {
			if !contains(args[i+1], "scale=") {
				t.Errorf("Expected -vf to contain 'scale=', got: %s", args[i+1])
			}
			if !contains(args[i+1], "format=yuv420p") {
				t.Errorf("Expected -vf to contain 'format=yuv420p', got: %s", args[i+1])
			}
			found = true
		}
	}
	if !found {
		t.Error("Expected -vf argument with scale filter")
	}
}

func TestMinFloat(t *testing.T) {
	tests := []struct {
		a, b, expected float64
	}{
		{5.0, 10.0, 5.0},
		{10.0, 5.0, 5.0},
		{5.0, 5.0, 5.0},
		{0.0, 5.0, 0.0},
		{3.5, 5.0, 3.5},
	}

	for _, tc := range tests {
		result := minFloat(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("minFloat(%v, %v) = %v, expected %v", tc.a, tc.b, result, tc.expected)
		}
	}
}

func TestMinInt(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{5, 10, 5},
		{10, 5, 5},
		{5, 5, 5},
		{0, 5, 0},
		{22050, 44100, 22050},
	}

	for _, tc := range tests {
		result := minInt(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("minInt(%v, %v) = %v, expected %v", tc.a, tc.b, result, tc.expected)
		}
	}
}

// Helper functions

func assertContains(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i, arg := range args {
		if arg == key && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("Expected args to contain %s %s, got: %v", key, value, args)
}

func assertNotContains(t *testing.T, args []string, key string) {
	t.Helper()
	for _, arg := range args {
		if arg == key {
			t.Errorf("Expected args NOT to contain %s, but it was found", key)
			return
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

