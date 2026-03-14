package fbprep

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/batch"
)

// BuildPrompt builds the user-turn prompt from metadata context and location tags.
func BuildPrompt(metadataCtx string, locationTags map[int]string) string {
	base := "## Metadata context\n\n" + metadataCtx
	if len(locationTags) > 0 {
		indices := make([]int, 0, len(locationTags))
		for i := range locationTags {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		lines := make([]string, 0, len(indices))
		for _, i := range indices {
			lines = append(lines, fmt.Sprintf("Item %d location (Maps-verified): %s", i, locationTags[i]))
		}
		base += "\n\n## Maps-verified locations\n" + strings.Join(lines, "\n")
	}
	return base + "\n\nGenerate the JSON array for each item in the same order as above."
}

// FilterLocationTagsForBatch returns a subset of locationTags for indices [baseIdx, baseIdx+count).
func FilterLocationTagsForBatch(locationTags map[int]string, baseIdx, count int) map[int]string {
	return batch.FilterByIndexRange(locationTags, baseIdx, count)
}

// MediaItemFromMap parses a map into MediaItem.
func MediaItemFromMap(m map[string]interface{}) MediaItem {
	return batch.MediaItemFromMap(m)
}

// BuildMediaPartsWithGCSURIs builds genai parts for a batch using pre-uploaded GCS URIs for videos.
func BuildMediaPartsWithGCSURIs(ctx context.Context, sessionID string, meta BatchMeta, gcsURIs map[int]string, deps SubmitDeps) ([]*genai.Part, error) {
	return batch.BuildMediaParts(ctx, sessionID, meta, gcsURIs, deps)
}
