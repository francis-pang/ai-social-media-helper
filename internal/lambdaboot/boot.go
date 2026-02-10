// Package lambdaboot provides shared Lambda cold-start bootstrap logic (DDR-053).
//
// Every Lambda in the project needs some subset of: AWS config, S3, DynamoDB,
// SSM parameter fetch, and startup logging. This package extracts the common
// init patterns so each Lambda's init() is a short composition of helpers.
package lambdaboot

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

// AWSClients holds the core AWS SDK clients used across Lambdas.
type AWSClients struct {
	Config aws.Config
	SSM    *ssm.Client
}

// S3Clients holds S3 client, presigner, and bucket name.
type S3Clients struct {
	Client    *s3.Client
	Presigner *s3.PresignClient
	Bucket    string
}

// InitAWS loads the default AWS config and returns it along with common clients.
func InitAWS() AWSClients {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}
	log.Debug().Str("region", cfg.Region).Msg("AWS config loaded")
	return AWSClients{
		Config: cfg,
		SSM:    ssm.NewFromConfig(cfg),
	}
}

// InitS3 creates an S3 client, presigner, and reads the bucket name from the
// given environment variable. Fatals if the env var is empty.
func InitS3(cfg aws.Config, bucketEnvVar string) S3Clients {
	client := s3.NewFromConfig(cfg)
	bucket := os.Getenv(bucketEnvVar)
	if bucket == "" {
		log.Fatal().Str("envVar", bucketEnvVar).Msg("Bucket environment variable is required")
	}
	return S3Clients{
		Client:    client,
		Presigner: s3.NewPresignClient(client),
		Bucket:    bucket,
	}
}

// InitDynamo creates a DynamoDB session store from the given config and
// table name environment variable. Fatals if the env var is empty.
func InitDynamo(cfg aws.Config, tableEnvVar string) *store.DynamoStore {
	tableName := os.Getenv(tableEnvVar)
	if tableName == "" {
		log.Fatal().Str("envVar", tableEnvVar).Msg("DynamoDB table environment variable is required")
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	return store.NewDynamoStore(ddbClient, tableName)
}

// InitDynamoOptional creates a DynamoDB session store if the env var is set.
// Returns nil (with a warning) if not configured.
func InitDynamoOptional(cfg aws.Config, tableEnvVar string) *store.DynamoStore {
	tableName := os.Getenv(tableEnvVar)
	if tableName == "" {
		log.Warn().Str("envVar", tableEnvVar).Msg("DynamoDB table not set — store disabled")
		return nil
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	return store.NewDynamoStore(ddbClient, tableName)
}

// LoadGeminiKey fetches the Gemini API key from SSM Parameter Store if not
// already set via GEMINI_API_KEY env var. Fatals on error.
func LoadGeminiKey(ssmClient *ssm.Client) {
	if os.Getenv("GEMINI_API_KEY") != "" {
		return
	}
	paramName := os.Getenv("SSM_API_KEY_PARAM")
	if paramName == "" {
		paramName = "/ai-social-media/prod/gemini-api-key"
	}
	ssmStart := time.Now()
	result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
		Name:           &paramName,
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
	}
	os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
	log.Debug().Str("param", paramName).Dur("elapsed", time.Since(ssmStart)).Msg("Gemini API key loaded from SSM")
}

// LoadInstagramCreds fetches Instagram access token and user ID from SSM
// Parameter Store. Returns an Instagram client if both are available, nil otherwise.
// Non-fatal: logs a warning if credentials are missing.
func LoadInstagramCreds(ssmClient *ssm.Client) *instagram.Client {
	igAccessToken := os.Getenv("INSTAGRAM_ACCESS_TOKEN")
	igUserID := os.Getenv("INSTAGRAM_USER_ID")

	if igAccessToken == "" || igUserID == "" {
		tokenParam := os.Getenv("SSM_INSTAGRAM_TOKEN_PARAM")
		if tokenParam == "" {
			tokenParam = "/ai-social-media/prod/instagram-access-token"
		}
		userIDParam := os.Getenv("SSM_INSTAGRAM_USER_ID_PARAM")
		if userIDParam == "" {
			userIDParam = "/ai-social-media/prod/instagram-user-id"
		}

		ssmStart := time.Now()
		tokenResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &tokenParam,
			WithDecryption: aws.Bool(true),
		})
		if err == nil {
			igAccessToken = *tokenResult.Parameter.Value
			log.Debug().Str("param", tokenParam).Dur("elapsed", time.Since(ssmStart)).Msg("Instagram token loaded from SSM")
		} else {
			log.Warn().Err(err).Str("param", tokenParam).Msg("Instagram access token not found in SSM — publishing disabled")
		}

		ssmStart = time.Now()
		userIDResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &userIDParam,
			WithDecryption: aws.Bool(false),
		})
		if err == nil {
			igUserID = *userIDResult.Parameter.Value
			log.Debug().Str("param", userIDParam).Dur("elapsed", time.Since(ssmStart)).Msg("Instagram user ID loaded from SSM")
		} else {
			log.Warn().Err(err).Str("param", userIDParam).Msg("Instagram user ID not found in SSM — publishing disabled")
		}
	}

	if igAccessToken != "" && igUserID != "" {
		client := instagram.NewClient(igAccessToken, igUserID)
		log.Info().Str("userId", igUserID).Msg("Instagram client initialized")
		return client
	}
	log.Warn().Msg("Instagram credentials not configured — publishing disabled")
	return nil
}

// StartupLog is a convenience wrapper for the startup logger.
func StartupLog(name string, initStart time.Time) *logging.StartupLogger {
	return logging.NewStartupLogger(name).InitDuration(time.Since(initStart))
}
