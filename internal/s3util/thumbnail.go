package s3util

import (
	"os"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
)

// GenerateThumbnailFromBytes writes raw image data to a temp file and delegates
// to filehandler.GenerateThumbnail. Replaces identical functions in
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
	mf := &filehandler.MediaFile{
		Path:     tmpPath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	return filehandler.GenerateThumbnail(mf, maxDimension)
}
