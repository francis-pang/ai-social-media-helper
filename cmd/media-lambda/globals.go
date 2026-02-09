package main

import (
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

	// Lambda client for async Worker Lambda invocations (DDR-050).
	lambdaClient    *lambda.Client
	workerLambdaArn string

	// Step Functions client for selection/enhancement pipelines (DDR-050).
	sfnClient         *sfn.Client
	selectionSfnArn   string
	enhancementSfnArn string

	// Instagram client for publishing (DDR-040).
	// nil if Instagram credentials are not configured (publishing disabled).
	igClient *instagram.Client
)
