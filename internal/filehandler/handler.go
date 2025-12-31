package filehandler

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evanoberholster/imagemeta"
	"github.com/rs/zerolog/log"
)

// SupportedImageExtensions defines the file extensions that are supported for image upload.
var SupportedImageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".heic": "image/heic",
	".heif": "image/heif",
}

// SupportedVideoExtensions defines the file extensions that are supported for video upload.
var SupportedVideoExtensions = map[string]string{
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
}

// Note: As of DDR-012, we always use the Files API for all media uploads.
// This provides consistent behavior, memory efficiency, and is the recommended
// approach for Gemini 3 Flash.

// MediaMetadata is the common interface for all media metadata types.
// Both ImageMetadata and VideoMetadata implement this interface.
type MediaMetadata interface {
	// FormatMetadataContext returns a formatted string for inclusion in AI prompts.
	FormatMetadataContext() string

	// GetMediaType returns "image" or "video".
	GetMediaType() string

	// HasGPSData returns true if GPS coordinates are available.
	HasGPSData() bool

	// GetGPS returns latitude and longitude (0,0 if not available).
	GetGPS() (latitude, longitude float64)

	// HasDateData returns true if date/time is available.
	HasDateData() bool

	// GetDate returns the date taken/created.
	GetDate() time.Time
}

// MediaFile represents a file that can be uploaded to Gemini.
// Note: Data is no longer populated; we always stream upload via Files API (DDR-012).
type MediaFile struct {
	Path     string
	MIMEType string
	Size     int64
	Metadata MediaMetadata
}

// ImageMetadata contains EXIF metadata extracted from an image.
type ImageMetadata struct {
	// GPS coordinates
	Latitude  float64
	Longitude float64
	HasGPS    bool

	// Timestamp
	DateTaken time.Time
	HasDate   bool

	// Camera info
	CameraMake  string
	CameraModel string

	// Raw fields for debugging
	RawFields map[string]string
}

// VideoMetadata contains metadata extracted from a video file.
type VideoMetadata struct {
	// GPS coordinates
	Latitude  float64
	Longitude float64
	HasGPS    bool

	// Timestamp
	CreateDate time.Time
	HasDate    bool

	// Video properties
	Duration    time.Duration
	Width       int
	Height      int
	FrameRate   float64
	Codec       string
	BitRate     int64
	ColorSpace  string
	AudioCodec  string
	AudioRate   int

	// Device info
	DeviceMake  string
	DeviceModel string
	Author      string

	// Raw fields for debugging
	RawFields map[string]string
}

// Ensure ImageMetadata implements MediaMetadata
var _ MediaMetadata = (*ImageMetadata)(nil)

// Ensure VideoMetadata implements MediaMetadata
var _ MediaMetadata = (*VideoMetadata)(nil)

// GetMediaType returns "image" for ImageMetadata.
func (m *ImageMetadata) GetMediaType() string {
	return "image"
}

// HasGPSData returns true if GPS coordinates are available.
func (m *ImageMetadata) HasGPSData() bool {
	return m.HasGPS
}

// GetGPS returns the GPS coordinates.
func (m *ImageMetadata) GetGPS() (latitude, longitude float64) {
	return m.Latitude, m.Longitude
}

// HasDateData returns true if date/time is available.
func (m *ImageMetadata) HasDateData() bool {
	return m.HasDate
}

// GetDate returns the date taken.
func (m *ImageMetadata) GetDate() time.Time {
	return m.DateTaken
}

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

// LoadMediaFile loads a media file from disk and returns a MediaFile struct.
// Note: File data is not loaded into memory; Files API streams directly from disk (DDR-012).
func LoadMediaFile(filePath string) (*MediaFile, error) {
	log.Debug().Str("path", filePath).Msg("Loading media file")

	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file: %s", filePath)
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(filePath))
	mimeType, err := GetMIMEType(ext)
	if err != nil {
		return nil, err
	}

	mediaFile := &MediaFile{
		Path:     filePath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	log.Info().
		Str("path", filePath).
		Str("mime_type", mimeType).
		Int64("size_bytes", info.Size()).
		Msg("Media file loaded successfully")

	// Extract metadata based on file type
	if IsImage(ext) {
		imgMeta, err := ExtractImageMetadata(filePath)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to extract image metadata, continuing without it")
		} else {
			mediaFile.Metadata = imgMeta
		}
	} else if IsVideo(ext) {
		vidMeta, err := ExtractVideoMetadata(filePath)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to extract video metadata, continuing without it")
		} else {
			mediaFile.Metadata = vidMeta
		}
	}

	return mediaFile, nil
}

// GetMIMEType returns the MIME type for a given file extension.
func GetMIMEType(ext string) (string, error) {
	ext = strings.ToLower(ext)

	if mimeType, ok := SupportedImageExtensions[ext]; ok {
		return mimeType, nil
	}

	if mimeType, ok := SupportedVideoExtensions[ext]; ok {
		return mimeType, nil
	}

	return "", fmt.Errorf("unsupported file extension: %s", ext)
}

// IsImage returns true if the file extension corresponds to an image.
func IsImage(ext string) bool {
	_, ok := SupportedImageExtensions[strings.ToLower(ext)]
	return ok
}

// IsVideo returns true if the file extension corresponds to a video.
func IsVideo(ext string) bool {
	_, ok := SupportedVideoExtensions[strings.ToLower(ext)]
	return ok
}

// IsSupported returns true if the file extension is supported (image or video).
func IsSupported(ext string) bool {
	return IsImage(ext) || IsVideo(ext)
}

// ExtractImageMetadata extracts EXIF metadata from an image file using the imagemeta library.
// This is a pure Go implementation that supports JPEG, HEIC, HEIF, and other formats.
func ExtractImageMetadata(filePath string) (*ImageMetadata, error) {
	log.Debug().Str("path", filePath).Msg("Extracting EXIF metadata using imagemeta library")

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Decode metadata using imagemeta
	exifData, err := imagemeta.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EXIF metadata: %w", err)
	}

	metadata := &ImageMetadata{
		RawFields: make(map[string]string),
	}

	// Extract GPS coordinates (GPS is a field, not a method)
	gps := exifData.GPS
	if gps.Latitude() != 0 || gps.Longitude() != 0 {
		metadata.Latitude = gps.Latitude()
		metadata.Longitude = gps.Longitude()
		metadata.HasGPS = true
		metadata.RawFields["GPSLatitude"] = fmt.Sprintf("%f", gps.Latitude())
		metadata.RawFields["GPSLongitude"] = fmt.Sprintf("%f", gps.Longitude())
	}

	// Extract date/time
	if !exifData.DateTimeOriginal().IsZero() {
		metadata.DateTaken = exifData.DateTimeOriginal()
		metadata.HasDate = true
		metadata.RawFields["DateTimeOriginal"] = exifData.DateTimeOriginal().String()
	} else if !exifData.CreateDate().IsZero() {
		metadata.DateTaken = exifData.CreateDate()
		metadata.HasDate = true
		metadata.RawFields["CreateDate"] = exifData.CreateDate().String()
	} else if !exifData.ModifyDate().IsZero() {
		metadata.DateTaken = exifData.ModifyDate()
		metadata.HasDate = true
		metadata.RawFields["ModifyDate"] = exifData.ModifyDate().String()
	}

	// Extract camera info (Make and Model are string fields)
	metadata.CameraMake = strings.TrimSpace(exifData.Make)
	metadata.CameraModel = strings.TrimSpace(exifData.Model)
	if metadata.CameraMake != "" {
		metadata.RawFields["Make"] = metadata.CameraMake
	}
	if metadata.CameraModel != "" {
		metadata.RawFields["Model"] = metadata.CameraModel
	}

	log.Info().
		Bool("has_gps", metadata.HasGPS).
		Float64("latitude", metadata.Latitude).
		Float64("longitude", metadata.Longitude).
		Bool("has_date", metadata.HasDate).
		Time("date_taken", metadata.DateTaken).
		Str("camera", metadata.CameraMake+" "+metadata.CameraModel).
		Msg("Image EXIF metadata extracted")

	return metadata, nil
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
// Requires ffprobe (part of FFmpeg) to be installed on the system.
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

	// Parse format tags
	for key, value := range probe.Format.Tags {
		metadata.RawFields[key] = value

		switch strings.ToLower(key) {
		case "creation_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.CreateDate = t
				metadata.HasDate = true
			}
		case "location", "location-eng":
			// Parse ISO 6709 format: "+38.0048-084.4848/"
			lat, lon := parseISO6709Location(value)
			if lat != 0 || lon != 0 {
				metadata.Latitude = lat
				metadata.Longitude = lon
				metadata.HasGPS = true
			}
		case "com.android.manufacturer", "make":
			metadata.DeviceMake = value
		case "com.android.model", "model":
			metadata.DeviceModel = value
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
			// Check stream tags for creation time
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
// Example: "+38.0048-084.4848/" -> (38.0048, -84.4848)
func parseISO6709Location(value string) (lat, lon float64) {
	// Remove trailing slash
	value = strings.TrimSuffix(value, "/")

	// Pattern: +/-DD.DDDD+/-DDD.DDDD
	pattern := regexp.MustCompile(`^([+-]?\d+\.?\d*?)([+-]\d+\.?\d*)$`)
	matches := pattern.FindStringSubmatch(value)

	if len(matches) == 3 {
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

// FormatMetadataContext formats the image metadata as a text block for inclusion in prompts.
func (m *ImageMetadata) FormatMetadataContext() string {
	var sb strings.Builder

	sb.WriteString("## EXTRACTED IMAGE METADATA\n\n")

	if m.HasGPS {
		sb.WriteString("**GPS Coordinates:**\n")
		sb.WriteString(fmt.Sprintf("- Latitude: %.6f\n", m.Latitude))
		sb.WriteString(fmt.Sprintf("- Longitude: %.6f\n", m.Longitude))
		sb.WriteString(fmt.Sprintf("- Google Maps: https://www.google.com/maps?q=%.6f,%.6f\n\n", m.Latitude, m.Longitude))
	} else {
		sb.WriteString("**GPS Coordinates:** Not available in image metadata\n\n")
	}

	if m.HasDate {
		sb.WriteString("**Date/Time Taken:**\n")
		sb.WriteString(fmt.Sprintf("- Date: %s\n", m.DateTaken.Format("Monday, January 2, 2006")))
		sb.WriteString(fmt.Sprintf("- Time: %s\n", m.DateTaken.Format("3:04 PM")))
		sb.WriteString(fmt.Sprintf("- Day of Week: %s\n\n", m.DateTaken.Weekday().String()))
	} else {
		sb.WriteString("**Date/Time Taken:** Not available in image metadata\n\n")
	}

	if m.CameraMake != "" || m.CameraModel != "" {
		sb.WriteString(fmt.Sprintf("**Camera:** %s %s\n\n", m.CameraMake, m.CameraModel))
	}

	return sb.String()
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

// CoordinatesToDMS converts decimal degrees to degrees, minutes, seconds format.
func CoordinatesToDMS(lat, lon float64) string {
	latDir := "N"
	if lat < 0 {
		latDir = "S"
		lat = -lat
	}

	lonDir := "E"
	if lon < 0 {
		lonDir = "W"
		lon = -lon
	}

	latDeg := int(lat)
	latMin := int((lat - float64(latDeg)) * 60)
	latSec := ((lat - float64(latDeg)) * 60 - float64(latMin)) * 60

	lonDeg := int(lon)
	lonMin := int((lon - float64(lonDeg)) * 60)
	lonSec := ((lon - float64(lonDeg)) * 60 - float64(lonMin)) * 60

	return fmt.Sprintf("%d°%d'%.2f\"%s, %d°%d'%.2f\"%s",
		latDeg, latMin, latSec, latDir,
		lonDeg, lonMin, lonSec, lonDir)
}
