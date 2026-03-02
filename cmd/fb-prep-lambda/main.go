package main

import (
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/bootstrap"
	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/store"
)

var (
	s3Client     *s3.Client
	mediaBucket  string
	sessionStore *store.DynamoStore
)

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := bootstrap.InitAWS()
	s3s := bootstrap.InitS3(awsClients.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	mediaBucket = s3s.Bucket
	sessionStore = bootstrap.InitDynamo(awsClients.Config, "DYNAMO_TABLE_NAME")
	bootstrap.LoadGeminiKey(awsClients.SSM)
	bootstrap.LoadGCPServiceAccountKey(awsClients.SSM)
	_ = ai.LoadGCPServiceAccount()

	bootstrap.StartupLog("fb-prep-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		Log()
}

func main() {
	lambda.Start(handler)
}
