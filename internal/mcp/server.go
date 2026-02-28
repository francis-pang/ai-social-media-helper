package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fpang/gemini-media-cli/internal/rag"
)

type ServerConfig struct {
	DDBClient     ProfileReader
	ProfilesTable string
	Media         *MediaConfig
}

type ProfileReader interface {
	GetPreferenceProfile(ctx context.Context) (*rag.PreferenceProfile, error)
}

func NewServer(cfg ServerConfig) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ai-social-media-rag",
		Version: "1.0.0",
	}, nil)

	registerRAGTools(server, cfg)

	if cfg.Media != nil {
		registerMediaTools(server, cfg.Media)
	}

	return server
}

func registerRAGTools(server *mcp.Server, cfg ServerConfig) {
	type profileArgs struct {
		QueryType string `json:"query_type" jsonschema:"Type of profile to retrieve,enum=triage,enum=selection"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_preference_profile",
		Description: "Retrieve the user's media curation preference profile. Describes patterns for keeping/discarding photos and videos based on past decisions. Call this when you need to understand user preferences for triage or selection.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args profileArgs) (*mcp.CallToolResult, any, error) {
		if cfg.DDBClient == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Profile reader not configured."}},
			}, nil, nil
		}
		profile, err := cfg.DDBClient.GetPreferenceProfile(ctx)
		if err != nil || profile == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No preference profile available yet."}},
			}, nil, nil
		}
		text := profile.ProfileText
		if text == "" {
			text = "No preference profile available yet."
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	type captionArgs struct{}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_caption_examples",
		Description: "Retrieve examples of the user's past social media captions. Use these to match their writing style, hashtag preferences, and tone when generating new captions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args captionArgs) (*mcp.CallToolResult, any, error) {
		if cfg.DDBClient == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Profile reader not configured."}},
			}, nil, nil
		}
		profile, err := cfg.DDBClient.GetPreferenceProfile(ctx)
		if err != nil || profile == nil || profile.CaptionExamplesText == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No past caption examples available yet."}},
			}, nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: profile.CaptionExamplesText}},
		}, nil, nil
	})

	type statsArgs struct{}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_curation_stats",
		Description: "Retrieve aggregate statistics about the user's media curation history: keep rate, override rate, common reasons for keeping/discarding, media type breakdown. Useful for calibrating triage thresholds.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args statsArgs) (*mcp.CallToolResult, any, error) {
		if cfg.DDBClient == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "Profile reader not configured."}},
			}, nil, nil
		}
		profile, err := cfg.DDBClient.GetPreferenceProfile(ctx)
		if err != nil || profile == nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No curation statistics available yet."}},
			}, nil, nil
		}
		statsJSON, _ := json.MarshalIndent(profile.Stats, "", "  ")
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(statsJSON)}},
		}, nil, nil
	})
}
