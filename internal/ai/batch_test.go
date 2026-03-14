package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestBatchJSONLExcludesSystemInstructionFromGenerationConfig(t *testing.T) {
	// Vertex AI rejects generationConfig containing systemInstruction.
	// Ensure our vertexGenConfig never serializes it.
	temp := float32(0.9)
	req := &genai.InlinedRequest{
		Model: "gemini-2.5-flash",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a helpful assistant."}},
			},
			Temperature: &temp,
		},
	}

	row := batchJSONLRow{
		Request: batchJSONLRequest{
			Contents: req.Contents,
		},
	}
	if req.Config != nil {
		row.Request.SystemInstruction = req.Config.SystemInstruction
		row.Request.GenerationConfig = toVertexGenConfig(req.Config)
	}

	line, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Parse and verify structure
	var parsed struct {
		Request struct {
			Contents          json.RawMessage `json:"contents"`
			GenerationConfig  json.RawMessage `json:"generationConfig"`
			SystemInstruction json.RawMessage `json:"systemInstruction"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// generationConfig must NOT contain systemInstruction
	if len(parsed.Request.GenerationConfig) > 0 {
		gcStr := string(parsed.Request.GenerationConfig)
		if strings.Contains(gcStr, "systemInstruction") {
			t.Errorf("generationConfig must not contain systemInstruction (Vertex AI rejects it); got: %s", gcStr)
		}
	}

	// systemInstruction must be present at request level
	if len(parsed.Request.SystemInstruction) == 0 {
		t.Error("systemInstruction must be present at request level")
	}
}
