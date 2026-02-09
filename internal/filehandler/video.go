package filehandler

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// VideoMetadata contains metadata extracted from a video file.
//
// This is the External Tool provider in the Split-Provider Model (DDR-013).
// It uses ffprobe (FFmpeg) because pure Go libraries for video metadata are
// low-level tools that provide raw atoms/boxes but don't automatically extract
// vendor-specific metadata like GPS.
//
// GPS location in videos is stored differently by each manufacturer:
//   - Apple: ©xyz in moov/udta (ISO 6709 format)
//   - Samsung: com.android.gps_latitude and com.android.gps_longitude
//   - DJI: Custom manufacturer atoms
//   - GoPro: GPMF telemetry stream
//
// ffprobe handles all these formats via its unified JSON output.
type VideoMetadata struct {
	// GPS coordinates (parsed from ISO 6709 or vendor-specific atoms)
	Latitude  float64
	Longitude float64
	HasGPS    bool

	// Timestamp
	CreateDate time.Time
	HasDate    bool

	// Video properties (from stream metadata)
	Duration   time.Duration
	Width      int
	Height     int
	FrameRate  float64
	Codec      string
	BitRate    int64
	ColorSpace string
	AudioCodec string
	AudioRate  int

	// Device info (from format tags)
	DeviceMake  string
	DeviceModel string
	Author      string

	// Raw fields for debugging
	RawFields map[string]string
}

// Ensure VideoMetadata implements MediaMetadata
var _ MediaMetadata = (*VideoMetadata)(nil)

// GetMediaType returns "video" for VideoMetadata.
func (m *VideoMetadata) GetMediaType() string {
	return "video"
}

// HasGPSData returns true if GPS coordinates are available.
func (m *VideoMetadata) HasGPSData() bool {
	return m.HasGPS
}

// GetGPS returns the GPS coordinates.
func (m *VideoMetadata) GetGPS() (latitude, longitude float64) {
	return m.Latitude, m.Longitude
}

// HasDateData returns true if date/time is available.
func (m *VideoMetadata) HasDateData() bool {
	return m.HasDate
}

// GetDate returns the create date.
func (m *VideoMetadata) GetDate() time.Time {
	return m.CreateDate
}

// CheckFFprobeAvailable checks if ffprobe is available in the system PATH.
// This can be called at startup to validate video metadata extraction capability.
// Returns nil if ffprobe is available, or an error describing the issue.
func CheckFFprobeAvailable() error {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return fmt.Errorf("ffprobe not found in PATH: video metadata extraction will be unavailable. Install FFmpeg with: brew install ffmpeg (macOS) or apt install ffmpeg (Linux)")
	}
	log.Debug().Str("path", path).Msg("ffprobe found")
	return nil
}

// IsFFprobeAvailable returns true if ffprobe is available in the system PATH.
// This is a convenience wrapper around CheckFFprobeAvailable for boolean checks.
func IsFFprobeAvailable() bool {
	return CheckFFprobeAvailable() == nil
}

// parseISO6709Location parses GPS coordinates in ISO 6709 format.
// Supports multiple formats:
//   - "+38.0048-084.4848/" (lat/lon only)
//   - "+37.7749-122.4194+000.000/" (lat/lon/altitude)
//   - "+40.7128-074.0060/" (New York style)
//
// ISO 6709 format: ±DD.DDDD±DDD.DDDD[±AAA.AAA]/
// Where latitude is ±DD to ±DD.DDDDDD and longitude is ±DDD to ±DDD.DDDDDD
func parseISO6709Location(value string) (lat, lon float64) {
	// Remove trailing slash
	value = strings.TrimSuffix(value, "/")

	// Pattern handles:
	// - Latitude: +/- followed by digits and optional decimal
	// - Longitude: +/- followed by digits and optional decimal
	// - Optional altitude: +/- followed by digits and optional decimal (ignored)
	pattern := regexp.MustCompile(`^([+-]\d+\.?\d*)([+-]\d+\.?\d*)(?:[+-]\d+\.?\d*)?$`)
	matches := pattern.FindStringSubmatch(value)

	if len(matches) >= 3 {
		lat, _ = strconv.ParseFloat(matches[1], 64)
		lon, _ = strconv.ParseFloat(matches[2], 64)
	}

	return lat, lon
}

// parseFrameRate parses frame rate from ffprobe format (e.g., "60/1" -> 60.0)
func parseFrameRate(value string) float64 {
	parts := strings.Split(value, "/")
	if len(parts) == 2 {
		num, _ := strconv.ParseFloat(parts[0], 64)
		den, _ := strconv.ParseFloat(parts[1], 64)
		if den != 0 {
			return num / den
		}
	}
	rate, _ := strconv.ParseFloat(value, 64)
	return rate
}

// formatDuration formats a duration in a human-readable format.
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// FormatMetadataContext formats the video metadata as a text block for inclusion in prompts.
func (m *VideoMetadata) FormatMetadataContext() string {
	var sb strings.Builder

	sb.WriteString("## EXTRACTED VIDEO METADATA\n\n")

	// GPS Information
	if m.HasGPS {
		sb.WriteString("**GPS Coordinates:**\n")
		sb.WriteString(fmt.Sprintf("- Latitude: %.6f\n", m.Latitude))
		sb.WriteString(fmt.Sprintf("- Longitude: %.6f\n", m.Longitude))
		sb.WriteString(fmt.Sprintf("- Google Maps: https://www.google.com/maps?q=%.6f,%.6f\n\n", m.Latitude, m.Longitude))
	} else {
		sb.WriteString("**GPS Coordinates:** Not available in video metadata\n\n")
	}

	// Date/Time Information
	if m.HasDate {
		sb.WriteString("**Date/Time Created:**\n")
		sb.WriteString(fmt.Sprintf("- Date: %s\n", m.CreateDate.Format("Monday, January 2, 2006")))
		sb.WriteString(fmt.Sprintf("- Time: %s\n", m.CreateDate.Format("3:04 PM")))
		sb.WriteString(fmt.Sprintf("- Day of Week: %s\n\n", m.CreateDate.Weekday().String()))
	} else {
		sb.WriteString("**Date/Time Created:** Not available in video metadata\n\n")
	}

	// Video Properties
	sb.WriteString("**Video Properties:**\n")
	if m.Duration > 0 {
		sb.WriteString(fmt.Sprintf("- Duration: %s\n", formatDuration(m.Duration)))
	}
	if m.Width > 0 && m.Height > 0 {
		sb.WriteString(fmt.Sprintf("- Resolution: %dx%d", m.Width, m.Height))
		if m.Width >= 3840 {
			sb.WriteString(" (4K UHD)")
		} else if m.Width >= 1920 {
			sb.WriteString(" (Full HD)")
		} else if m.Width >= 1280 {
			sb.WriteString(" (HD)")
		}
		sb.WriteString("\n")
	}
	if m.FrameRate > 0 {
		sb.WriteString(fmt.Sprintf("- Frame Rate: %.2f fps\n", m.FrameRate))
	}
	if m.Codec != "" {
		sb.WriteString(fmt.Sprintf("- Video Codec: %s\n", m.Codec))
	}
	if m.BitRate > 0 {
		sb.WriteString(fmt.Sprintf("- Bit Rate: %.2f Mbps\n", float64(m.BitRate)/(1024*1024)))
	}
	if m.ColorSpace != "" {
		sb.WriteString(fmt.Sprintf("- Color Space: %s\n", m.ColorSpace))
	}
	sb.WriteString("\n")

	// Audio Properties
	if m.AudioCodec != "" || m.AudioRate > 0 {
		sb.WriteString("**Audio Properties:**\n")
		if m.AudioCodec != "" {
			sb.WriteString(fmt.Sprintf("- Codec: %s\n", m.AudioCodec))
		}
		if m.AudioRate > 0 {
			sb.WriteString(fmt.Sprintf("- Sample Rate: %d Hz\n", m.AudioRate))
		}
		sb.WriteString("\n")
	}

	// Device Information
	if m.DeviceMake != "" || m.DeviceModel != "" || m.Author != "" {
		sb.WriteString("**Recording Device:**\n")
		if m.Author != "" {
			sb.WriteString(fmt.Sprintf("- Device: %s\n", m.Author))
		}
		if m.DeviceMake != "" {
			sb.WriteString(fmt.Sprintf("- Make: %s\n", m.DeviceMake))
		}
		if m.DeviceModel != "" {
			sb.WriteString(fmt.Sprintf("- Model: %s\n", m.DeviceModel))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
