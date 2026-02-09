package main

import (
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/fpang/gemini-media-cli/internal/instagram"
)

// AWS clients initialized at cold start.
var (
	s3Client           *s3.Client
	presigner          *s3.PresignClient
	mediaBucket        string
	originVerifySecret string // DDR-028: shared secret for CloudFront origin verification

	// Instagram client for publishing (DDR-040).
	// nil if Instagram credentials are not configured (publishing disabled).
	igClient *instagram.Client
)
