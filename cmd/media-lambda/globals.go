package main

import (
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/store"
)

// AWS clients initialized at cold start.
var (
	s3Client           *s3.Client
	presigner          *s3.PresignClient
	mediaBucket        string
	originVerifySecret string // DDR-028: shared secret for CloudFront origin verification

	// DynamoDB session store for persistent job state (DDR-050).
	sessionStore *store.DynamoStore

	// File processing store for per-file status during triage (DDR-061).
	fileProcessStore *store.FileProcessingStore

	// Lambda client for async Lambda invocations (DDR-050, DDR-053).
	lambdaClient *lambda.Client

	// Domain-specific Lambda ARNs for async dispatch (DDR-053).
	descriptionLambdaArn string
	downloadLambdaArn    string
	enhanceLambdaArn     string

	// Step Functions client for pipelines (DDR-050, DDR-052).
	sfnClient         *sfn.Client
	selectionSfnArn   string
	enhancementSfnArn string
	triageSfnArn      string // DDR-052: Triage Pipeline
	publishSfnArn     string // DDR-052: Publish Pipeline

	// Instagram client for publishing (DDR-040).
	// nil if Instagram credentials are not configured (publishing disabled).
	igClient *instagram.Client

	// EventBridge client for RAG feedback events (override capture).
	ebClient *eventbridge.Client
)
