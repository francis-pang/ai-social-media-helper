package filehandler

import (
	"encoding/json"
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

// ffprobeOutput represents the JSON structure from ffprobe.
type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeFormat struct {
	Filename       string            `json:"filename"`
	Duration       string            `json:"duration"`
	Size           string            `json:"size"`
	BitRate        string            `json:"bit_rate"`
	FormatName     string            `json:"format_name"`
	FormatLongName string            `json:"format_long_name"`
	Tags           map[string]string `json:"tags"`
}

type ffprobeStream struct {
	Index         int               `json:"index"`
	CodecName     string            `json:"codec_name"`
	CodecLongName string            `json:"codec_long_name"`
	CodecType     string            `json:"codec_type"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	RFrameRate    string            `json:"r_frame_rate"`
	AvgFrameRate  string            `json:"avg_frame_rate"`
	Duration      string            `json:"duration"`
	BitRate       string            `json:"bit_rate"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ColorSpace    string            `json:"color_space"`
	Tags          map[string]string `json:"tags"`
}

// ExtractVideoMetadata extracts metadata from a video file using ffprobe.
//
// Requires ffprobe (part of FFmpeg) to be installed on the system.
// This is the recommended approach for video metadata because:
//   - Handles all container formats (MP4, MOV, MKV, AVI, WebM)
//   - Extracts vendor-specific GPS atoms (Apple, Samsung, DJI, GoPro)
//   - Provides stream properties (resolution, codec, frame rate)
//   - Returns clean JSON output for easy parsing
//
// The function extracts metadata from both format-level tags and stream-level tags,
// with appropriate fallbacks for each field.
func ExtractVideoMetadata(filePath string) (*VideoMetadata, error) {
	log.Debug().Str("path", filePath).Msg("Extracting video metadata using ffprobe")

	// Check if ffprobe is available
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found in PATH: %w", err)
	}

	// Run ffprobe with JSON output
	cmd := exec.Command(ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse JSON output
	var probe ffprobeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	metadata := &VideoMetadata{
		RawFields: make(map[string]string),
	}

	// Extract format-level metadata
	if probe.Format.Duration != "" {
		if dur, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
			metadata.Duration = time.Duration(dur * float64(time.Second))
		}
	}
	if probe.Format.BitRate != "" {
		metadata.BitRate, _ = strconv.ParseInt(probe.Format.BitRate, 10, 64)
	}

	// Parse format tags (vendor-specific metadata lives here)
	for key, value := range probe.Format.Tags {
		metadata.RawFields[key] = value

		switch strings.ToLower(key) {
		case "creation_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.CreateDate = t
				metadata.HasDate = true
			}
		case "location", "location-eng", "com.apple.quicktime.location.iso6709":
			// Parse ISO 6709 format: "+38.0048-084.4848/" or "+37.7749-122.4194+000.000/"
			// Apple uses ©xyz atom which ffprobe exports as location or com.apple.quicktime.location.ISO6709
			if !metadata.HasGPS {
				lat, lon := parseISO6709Location(value)
				if lat != 0 || lon != 0 {
					metadata.Latitude = lat
					metadata.Longitude = lon
					metadata.HasGPS = true
				}
			}
		case "com.android.manufacturer", "make", "com.apple.quicktime.make":
			if metadata.DeviceMake == "" {
				metadata.DeviceMake = value
			}
		case "com.android.model", "model", "com.apple.quicktime.model":
			if metadata.DeviceModel == "" {
				metadata.DeviceModel = value
			}
		case "com.android.version":
			metadata.RawFields["AndroidVersion"] = value
		case "com.apple.quicktime.software":
			metadata.RawFields["Software"] = value
		}
	}

	// Extract stream-level metadata
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			if metadata.Width == 0 {
				metadata.Width = stream.Width
				metadata.Height = stream.Height
			}
			if metadata.Codec == "" {
				metadata.Codec = stream.CodecName
			}
			if metadata.ColorSpace == "" && stream.ColorSpace != "" {
				metadata.ColorSpace = stream.ColorSpace
			}
			if metadata.FrameRate == 0 && stream.RFrameRate != "" {
				metadata.FrameRate = parseFrameRate(stream.RFrameRate)
			}
			// Check stream tags for creation time (fallback)
			if !metadata.HasDate {
				if ct, ok := stream.Tags["creation_time"]; ok {
					if t, err := time.Parse(time.RFC3339, ct); err == nil {
						metadata.CreateDate = t
						metadata.HasDate = true
					}
				}
			}
		case "audio":
			if metadata.AudioCodec == "" {
				metadata.AudioCodec = stream.CodecName
			}
			if metadata.AudioRate == 0 && stream.SampleRate != "" {
				metadata.AudioRate, _ = strconv.Atoi(stream.SampleRate)
			}
		}
	}

	log.Info().
		Bool("has_gps", metadata.HasGPS).
		Float64("latitude", metadata.Latitude).
		Float64("longitude", metadata.Longitude).
		Bool("has_date", metadata.HasDate).
		Time("create_date", metadata.CreateDate).
		Dur("duration", metadata.Duration).
		Int("width", metadata.Width).
		Int("height", metadata.Height).
		Float64("frame_rate", metadata.FrameRate).
		Str("codec", metadata.Codec).
		Str("device", metadata.DeviceMake+" "+metadata.DeviceModel).
		Msg("Video metadata extracted via ffprobe")

	return metadata, nil
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

