package filehandler

// video_extract.go contains the ffprobe-based video metadata extraction logic.
// See video.go for the VideoMetadata type definition and interface methods.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

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
