package main

import (
	"context"
	"log"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/fpang/gemini-media-cli/internal/mcp"
)

func main() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}

	ddbClient := dynamodb.NewFromConfig(cfg)

	tableName := os.Getenv("RAG_PROFILES_TABLE_NAME")
	if tableName == "" {
		tableName = "rag-preference-profiles"
	}

	server := mcpserver.NewServer(mcpserver.ServerConfig{
		DDBClient: &mcpserver.DDBProfileReader{
			Client:    ddbClient,
			TableName: tableName,
		},
		ProfilesTable: tableName,
	})

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("MCP server error: %v", err)
	}
}
