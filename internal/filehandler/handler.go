package filehandler

import (
	"fmt"
	"os"
	"path/filepath"
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

// MediaFile represents a file that can be uploaded to Gemini.
type MediaFile struct {
	Path     string
	MIMEType string
	Data     []byte
	Size     int64
	Metadata *ImageMetadata
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

// LoadMediaFile loads a media file from disk and returns a MediaFile struct.
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

	// Read file data
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	log.Info().
		Str("path", filePath).
		Str("mime_type", mimeType).
		Int64("size_bytes", info.Size()).
		Msg("Media file loaded successfully")

	// Extract EXIF metadata if this is an image
	var metadata *ImageMetadata
	if IsImage(ext) {
		var err error
		metadata, err = ExtractMetadata(filePath)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to extract EXIF metadata, continuing without it")
		}
	}

	return &MediaFile{
		Path:     filePath,
		MIMEType: mimeType,
		Data:     data,
		Size:     info.Size(),
		Metadata: metadata,
	}, nil
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

// ExtractMetadata extracts EXIF metadata from an image file using the imagemeta library.
// This is a pure Go implementation that supports JPEG, HEIC, HEIF, and other formats.
func ExtractMetadata(filePath string) (*ImageMetadata, error) {
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
		Msg("EXIF metadata extracted")

	return metadata, nil
}

// FormatMetadataContext formats the metadata as a text block for inclusion in prompts.
func (m *ImageMetadata) FormatMetadataContext() string {
	var sb strings.Builder

	sb.WriteString("## EXTRACTED EXIF METADATA\n\n")

	if m.HasGPS {
		sb.WriteString(fmt.Sprintf("**GPS Coordinates:**\n"))
		sb.WriteString(fmt.Sprintf("- Latitude: %.6f\n", m.Latitude))
		sb.WriteString(fmt.Sprintf("- Longitude: %.6f\n", m.Longitude))
		sb.WriteString(fmt.Sprintf("- Google Maps: https://www.google.com/maps?q=%.6f,%.6f\n\n", m.Latitude, m.Longitude))
	} else {
		sb.WriteString("**GPS Coordinates:** Not available in image metadata\n\n")
	}

	if m.HasDate {
		sb.WriteString(fmt.Sprintf("**Date/Time Taken:**\n"))
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


