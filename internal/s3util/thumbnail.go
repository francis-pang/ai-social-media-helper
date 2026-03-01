package s3util

import (
	"os"

	"github.com/fpang/ai-social-media-helper/internal/media"
)

// GenerateThumbnailFromBytes writes raw image data to a temp file and delegates
// to media.GenerateThumbnail. Replaces identical functions in
// enhance-lambda and description-lambda.
func GenerateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	tmpFile, err := os.CreateTemp("", "thumb-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(imageData); err != nil {
		tmpFile.Close()
		return nil, "", err
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	mf := &media.MediaFile{
		Path:     tmpPath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	return media.GenerateThumbnail(mf, maxDimension)
}
