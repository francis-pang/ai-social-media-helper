// Package jsonutil provides utilities for extracting and parsing JSON from
// LLM responses that may be wrapped in markdown code fences or embedded in prose.
package jsonutil

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StripMarkdownFences removes ```json ... ``` or ``` ... ``` wrapping from text.
// Returns the content between the fences, or the original text if no fences are found.
func StripMarkdownFences(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}

	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return text
	}

	startIdx := 1 // skip the opening ``` line
	endIdx := len(lines) - 1

	// Find the closing ```
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "```" {
			endIdx = i
			break
		}
	}

	return strings.Join(lines[startIdx:endIdx], "\n")
}

// ExtractJSON finds and returns the JSON content (object or array) from text
// that may contain surrounding non-JSON content.
// It finds the first { or [ and matches it with the last corresponding } or ].
func ExtractJSON(text string) (string, error) {
	text = strings.TrimSpace(text)

	objIdx := strings.Index(text, "{")
	arrIdx := strings.Index(text, "[")

	if objIdx == -1 && arrIdx == -1 {
		return "", fmt.Errorf("no JSON content found")
	}

	// Determine which delimiter comes first
	var startIdx int
	var endChar string

	if arrIdx == -1 || (objIdx != -1 && objIdx <= arrIdx) {
		startIdx = objIdx
		endChar = "}"
	} else {
		startIdx = arrIdx
		endChar = "]"
	}

	text = text[startIdx:]
	endIdx := strings.LastIndex(text, endChar)
	if endIdx == -1 {
		return "", fmt.Errorf("no closing %s found", endChar)
	}

	return text[:endIdx+1], nil
}

// ParseJSON strips markdown fences from raw LLM response text, extracts JSON
// content (object or array), and unmarshals it into the provided type T.
//
// This consolidates the common pattern of parsing JSON from Gemini API responses
// that may be wrapped in markdown code fences or embedded in prose.
func ParseJSON[T any](raw string) (T, error) {
	text := StripMarkdownFences(raw)
	jsonStr, err := ExtractJSON(text)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%w (raw length: %d)", err, len(raw))
	}

	var result T
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		var zero T
		// Include a truncated preview in the error for debugging
		preview := jsonStr
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return zero, fmt.Errorf("invalid JSON: %w (text: %s)", err, preview)
	}
	return result, nil
}
