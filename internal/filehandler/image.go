package filehandler

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/evanoberholster/imagemeta"
	"github.com/rs/zerolog/log"
)

// ImageMetadata contains EXIF metadata extracted from an image.
//
// This is the Pure Go provider in the Split-Provider Model (DDR-013).
// It uses evanoberholster/imagemeta which supports:
//   - HEIC (parses BMFF container to find EXIF block)
//   - JPEG (standard EXIF at file start)
//   - TIFF (standard IFD structure)
//   - PNG/WebP (graceful handling of limited metadata)
//
// The library uses io.Reader/io.Seeker pattern for memory efficiency,
// reading only ~64KB of metadata from a 20MB photo.
type ImageMetadata struct {
	// GPS coordinates (converted from EXIF Rational format to float64)
	Latitude  float64
	Longitude float64
	HasGPS    bool

	// Timestamp (with timezone if available in OffsetTimeOriginal)
	DateTaken time.Time
	HasDate   bool

	// Camera info
	CameraMake  string
	CameraModel string

	// Raw fields for debugging
	RawFields map[string]string
}

// Ensure ImageMetadata implements MediaMetadata
var _ MediaMetadata = (*ImageMetadata)(nil)

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

// ExtractImageMetadata extracts EXIF metadata from an image file using the imagemeta library.
//
// This is a pure Go implementation that supports JPEG, HEIC, HEIF, TIFF, and other formats.
// It uses the io.Reader pattern for memory efficiency—only metadata bytes are read,
// not the entire image file.
//
// For HEIC files, the library:
//  1. Parses the ftyp box to identify the container type
//  2. Navigates the meta box hierarchy
//  3. Locates the iloc (item location) and iprp (item properties) boxes
//  4. Extracts the raw EXIF bytes from the appropriate item
//  5. Parses EXIF data using standard IFD (Image File Directory) structure
//
// GPS coordinates are stored in EXIF as "Rational" values (pairs of 32-bit integers).
// The library handles the conversion to float64 including reference direction (N/S, E/W).
func ExtractImageMetadata(filePath string) (*ImageMetadata, error) {
	log.Debug().Str("path", filePath).Msg("Extracting EXIF metadata using imagemeta library")

	// Open the file - uses io.Reader pattern for memory efficiency
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Decode metadata using imagemeta
	// This auto-detects format (JPEG, HEIC, TIFF) from file headers
	exifData, err := imagemeta.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode EXIF metadata: %w", err)
	}

	metadata := &ImageMetadata{
		RawFields: make(map[string]string),
	}

	// Extract GPS coordinates
	// GPS is stored as Rational values (e.g., 40° 44' 55" = [40/1, 44/1, 550404/10000])
	// The library handles the conversion and reference direction (N/S, E/W)
	gps := exifData.GPS
	if gps.Latitude() != 0 || gps.Longitude() != 0 {
		metadata.Latitude = gps.Latitude()
		metadata.Longitude = gps.Longitude()
		metadata.HasGPS = true
		metadata.RawFields["GPSLatitude"] = fmt.Sprintf("%f", gps.Latitude())
		metadata.RawFields["GPSLongitude"] = fmt.Sprintf("%f", gps.Longitude())
	}

	// Extract date/time with fallback chain
	// Priority: DateTimeOriginal > CreateDate > ModifyDate
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

	// Extract camera info
	metadata.CameraMake = strings.TrimSpace(exifData.Make)
	metadata.CameraModel = strings.TrimSpace(exifData.Model)
	if metadata.CameraMake != "" {
		metadata.RawFields["Make"] = metadata.CameraMake
	}
	if metadata.CameraModel != "" {
		metadata.RawFields["Model"] = metadata.CameraModel
	}

	log.Debug().
		Str("path", filePath).
		Bool("has_gps", metadata.HasGPS).
		Bool("has_date", metadata.HasDate).
		Msg("Image metadata extraction complete")

	return metadata, nil
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

