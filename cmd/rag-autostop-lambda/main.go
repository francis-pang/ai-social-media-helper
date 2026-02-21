package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/rs/zerolog/log"
)

const idleTimeout = 2 * time.Hour

var rdsClient *rds.Client
var dynamoClient *dynamodb.Client
var clusterARN string
var tableName string

type lastActivityRecord struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	Timestamp string `dynamodbav:"timestamp"`
}

func handler(ctx context.Context) error {
	if clusterARN == "" || tableName == "" {
		log.Warn().Msg("AURORA_CLUSTER_ARN or RAG_PROFILES_TABLE_NAME not configured, skipping")
		return nil
	}

	// Read lastActivity from DynamoDB: PK="ACTIVITY#aurora", SK="lastActivity"
	out, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "ACTIVITY#aurora"},
			"SK": &types.AttributeValueMemberS{Value: "lastActivity"},
		},
	})
	if err != nil {
		log.Error().Err(err).Msg("GetItem failed")
		return err
	}

	if out.Item == nil {
		log.Info().Msg("No lastActivity record found, skipping autostop")
		return nil
	}

	var rec lastActivityRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		log.Error().Err(err).Msg("UnmarshalMap failed")
		return err
	}

	if rec.Timestamp == "" {
		log.Info().Msg("lastActivity timestamp empty, skipping autostop")
		return nil
	}

	lastActivity, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil {
		log.Error().Err(err).Str("timestamp", rec.Timestamp).Msg("Failed to parse timestamp")
		return err
	}

	idleDuration := time.Since(lastActivity)
	if idleDuration < idleTimeout {
		log.Debug().
			Dur("idleDuration", idleDuration).
			Dur("idleTimeout", idleTimeout).
			Msg("Cluster not idle long enough, skipping")
		return nil
	}

	// Check cluster status and stop if available
	clusterID := clusterARN
	if idx := strings.LastIndex(clusterARN, ":"); idx >= 0 && idx < len(clusterARN)-1 {
		clusterID = clusterARN[idx+1:]
	}

	descOut, err := rdsClient.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		log.Error().Err(err).Msg("DescribeDBClusters failed")
		return err
	}

	if len(descOut.DBClusters) == 0 {
		log.Warn().Str("clusterId", clusterID).Msg("DB cluster not found")
		return nil
	}

	status := aws.ToString(descOut.DBClusters[0].Status)
	if status != "available" {
		log.Info().Str("status", status).Msg("Cluster not available, skipping stop")
		return nil
	}

	_, err = rdsClient.StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		log.Error().Err(err).Msg("StopDBCluster failed")
		return err
	}

	log.Info().
		Str("clusterId", clusterID).
		Dur("idleDuration", idleDuration).
		Msg("Stopped Aurora cluster due to idle timeout")
	return nil
}

func main() {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	rdsClient = rds.NewFromConfig(cfg)
	dynamoClient = dynamodb.NewFromConfig(cfg)
	clusterARN = os.Getenv("AURORA_CLUSTER_ARN")
	tableName = os.Getenv("RAG_PROFILES_TABLE_NAME")

	lambda.Start(handler)
}
