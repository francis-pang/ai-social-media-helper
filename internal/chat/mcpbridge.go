package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

type MCPBridge struct {
	session *mcp.ClientSession
	tools   []*genai.Tool
}

func NewMCPBridge(ctx context.Context, server *mcp.Server) (*MCPBridge, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "gemini-bridge",
		Version: "1.0.0",
	}, nil)

	ct, st := mcp.NewInMemoryTransports()

	go func() {
		if err := server.Run(ctx, st); err != nil {
			log.Warn().Err(err).Msg("MCP server Run error")
		}
	}()

	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("MCP client connect: %w", err)
	}

	toolListResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	var decls []*genai.FunctionDeclaration
	for _, tool := range toolListResult.Tools {
		decls = append(decls, convertMCPToolToGemini(tool))
	}

	return &MCPBridge{
		session: session,
		tools:   []*genai.Tool{{FunctionDeclarations: decls}},
	}, nil
}

func (b *MCPBridge) Close() {
	if b.session != nil {
		b.session.Close()
	}
}

func (b *MCPBridge) GeminiTools() []*genai.Tool {
	return b.tools
}

func (b *MCPBridge) HandleFunctionCall(ctx context.Context, call *genai.FunctionCall) ([]*genai.Part, error) {
	result, err := b.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      call.Name,
		Arguments: call.Args,
	})
	if err != nil {
		return errorParts(call.Name, err.Error()), nil
	}

	var parts []*genai.Part
	var textSummary strings.Builder

	for _, c := range result.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			var ref mediaRef
			if json.Unmarshal([]byte(v.Text), &ref) == nil && ref.MediaRef == "video" {
				parts = append(parts, &genai.Part{
					FileData: &genai.FileData{
						FileURI:  ref.URI,
						MIMEType: ref.MIMEType,
					},
				})
			} else {
				textSummary.WriteString(v.Text)
				textSummary.WriteByte('\n')
			}

		case *mcp.ImageContent:
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: v.MIMEType,
					Data:     v.Data,
				},
			})
		}
	}

	funcRespPart := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			Name:     call.Name,
			Response: map[string]any{"summary": textSummary.String()},
		},
	}
	parts = append([]*genai.Part{funcRespPart}, parts...)

	return parts, nil
}

func errorParts(name, errMsg string) []*genai.Part {
	return []*genai.Part{{
		FunctionResponse: &genai.FunctionResponse{
			Name:     name,
			Response: map[string]any{"error": errMsg},
		},
	}}
}

type mediaRef struct {
	MediaRef string `json:"_media_ref"`
	URI      string `json:"uri"`
	MIMEType string `json:"mime_type"`
	Filename string `json:"filename"`
}

func convertMCPToolToGemini(tool *mcp.Tool) *genai.FunctionDeclaration {
	decl := &genai.FunctionDeclaration{
		Name:        tool.Name,
		Description: tool.Description,
	}
	if tool.InputSchema != nil {
		decl.Parameters = jsonSchemaToGeminiSchema(tool.InputSchema)
	}
	return decl
}

func jsonSchemaToGeminiSchema(schema any) *genai.Schema {
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return convertSchemaMap(raw)
}

func convertSchemaMap(m map[string]any) *genai.Schema {
	s := &genai.Schema{}

	if t, ok := m["type"].(string); ok {
		switch t {
		case "object":
			s.Type = genai.TypeObject
		case "string":
			s.Type = genai.TypeString
		case "integer":
			s.Type = genai.TypeInteger
		case "number":
			s.Type = genai.TypeNumber
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		}
	}

	if desc, ok := m["description"].(string); ok {
		s.Description = desc
	}

	if props, ok := m["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema)
		for k, v := range props {
			if propMap, ok := v.(map[string]any); ok {
				s.Properties[k] = convertSchemaMap(propMap)
			}
		}
	}

	if required, ok := m["required"].([]any); ok {
		for _, r := range required {
			if rs, ok := r.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}

	if items, ok := m["items"].(map[string]any); ok {
		s.Items = convertSchemaMap(items)
	}

	if enums, ok := m["enum"].([]any); ok {
		for _, e := range enums {
			if es, ok := e.(string); ok {
				s.Enum = append(s.Enum, es)
			}
		}
	}

	return s
}

func extractFunctionCalls(resp *genai.GenerateContentResponse) []*genai.FunctionCall {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil
	}
	var calls []*genai.FunctionCall
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.FunctionCall != nil {
			calls = append(calls, part.FunctionCall)
		}
	}
	return calls
}

func GenerateWithMCP(
	ctx context.Context,
	client *genai.Client,
	bridge *MCPBridge,
	model string,
	contents []*genai.Content,
	config *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	config.Tools = bridge.GeminiTools()

	resp, err := client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, err
	}

	const maxRounds = 3
	for round := 0; round < maxRounds; round++ {
		calls := extractFunctionCalls(resp)
		if len(calls) == 0 {
			return resp, nil
		}

		var allParts []*genai.Part
		for _, call := range calls {
			parts, _ := bridge.HandleFunctionCall(ctx, call)
			allParts = append(allParts, parts...)
		}

		contents = append(contents, resp.Candidates[0].Content)
		contents = append(contents, &genai.Content{Role: "user", Parts: allParts})

		resp, err = client.Models.GenerateContent(ctx, model, contents, config)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}
