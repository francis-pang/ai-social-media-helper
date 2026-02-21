package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/rs/zerolog/log"
)

var rdsClient *rds.Client
var clusterARN string

func handler(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if clusterARN == "" {
		log.Error().Msg("AURORA_CLUSTER_ARN not configured")
		return jsonResponse(500, map[string]string{"status": "unknown", "error": "AURORA_CLUSTER_ARN not configured"})
	}

	// Use ARN or extract identifier (DescribeDBClusters accepts either)
	clusterID := clusterARN
	if idx := strings.LastIndex(clusterARN, ":"); idx >= 0 && idx < len(clusterARN)-1 {
		clusterID = clusterARN[idx+1:]
	}

	out, err := rdsClient.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		log.Error().Err(err).Msg("DescribeDBClusters failed")
		return jsonResponse(500, map[string]string{"status": "unknown", "error": err.Error()})
	}

	if len(out.DBClusters) == 0 {
		log.Warn().Str("clusterId", clusterID).Msg("DB cluster not found")
		return jsonResponse(200, map[string]string{"status": "not-found"})
	}

	status := aws.ToString(out.DBClusters[0].Status)
	switch status {
	case "stopped":
		_, err := rdsClient.StartDBCluster(ctx, &rds.StartDBClusterInput{
			DBClusterIdentifier: aws.String(clusterID),
		})
		if err != nil {
			log.Error().Err(err).Msg("StartDBCluster failed")
			return jsonResponse(500, map[string]string{"status": "unknown", "error": err.Error()})
		}
		log.Info().Str("clusterId", clusterID).Msg("Started Aurora cluster")
		return jsonResponse(200, map[string]string{"status": "starting"})
	case "starting", "configuring-enhanced-monitoring":
		return jsonResponse(200, map[string]string{"status": "starting"})
	case "available":
		return jsonResponse(200, map[string]string{"status": "available"})
	default:
		return jsonResponse(200, map[string]string{"status": status})
	}
}

func jsonResponse(statusCode int, body map[string]string) (events.APIGatewayV2HTTPResponse, error) {
	b, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Body:       string(b),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

func main() {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	rdsClient = rds.NewFromConfig(cfg)
	clusterARN = os.Getenv("AURORA_CLUSTER_ARN")
	if clusterARN == "" {
		clusterARN = os.Getenv("AURORA_CLUSTER_IDENTIFIER")
	}

	lambda.Start(handler)
}
