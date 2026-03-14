package fbprep

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fpang/ai-social-media-helper/internal/store"
)

// ResponseItem matches the JSON output format from the FB Prep AI model.
type ResponseItem struct {
	ItemIndex          int    `json:"item_index"`
	Caption            string `json:"caption"`
	LocationTag        string `json:"location_tag"`
	DateTimestamp      string `json:"date_timestamp"`
	LocationConfidence string `json:"location_confidence"`
}

// ParseResponse parses the Gemini response text (JSON array, single object, or JSONL)
// into FBPrepItems. s3Keys maps item_index to S3 key.
func ParseResponse(responseText string, s3Keys []string) ([]store.FBPrepItem, error) {
	text := strings.TrimSpace(responseText)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var filtered []string
		for _, line := range lines {
			if line == "```" || line == "```json" {
				continue
			}
			filtered = append(filtered, line)
		}
		text = strings.Join(filtered, "\n")
	}

	var raw []ResponseItem
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		var single ResponseItem
		if singleErr := json.Unmarshal([]byte(text), &single); singleErr == nil {
			raw = []ResponseItem{single}
		} else {
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj ResponseItem
				if json.Unmarshal([]byte(line), &obj) == nil {
					raw = append(raw, obj)
				}
			}
			if len(raw) == 0 {
				return nil, fmt.Errorf("parse JSON: %w", err)
			}
		}
	}

	items := make([]store.FBPrepItem, 0, len(raw))
	for _, r := range raw {
		s3Key := ""
		if r.ItemIndex >= 0 && r.ItemIndex < len(s3Keys) {
			s3Key = s3Keys[r.ItemIndex]
		}
		items = append(items, store.FBPrepItem{
			ItemIndex:          r.ItemIndex,
			S3Key:              s3Key,
			Key:                s3Key,
			Caption:            r.Caption,
			LocationTag:        r.LocationTag,
			DateTimestamp:      r.DateTimestamp,
			LocationConfidence: r.LocationConfidence,
		})
	}
	return items, nil
}
