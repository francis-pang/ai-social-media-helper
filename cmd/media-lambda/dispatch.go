package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/rs/zerolog/log"
)

// invokeWorkerAsync sends an event to the Worker Lambda asynchronously (DDR-050).
// Uses InvocationType=Event so the API Lambda returns immediately without
// waiting for the Worker Lambda to process the job.
func invokeWorkerAsync(ctx context.Context, event map[string]interface{}) error {
	if lambdaClient == nil || workerLambdaArn == "" {
		log.Warn().Msg("Worker Lambda client not configured")
		return fmt.Errorf("worker lambda not configured")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal worker event")
		return fmt.Errorf("marshal worker event: %w", err)
	}

	log.Debug().Int("payloadSize", len(payload)).Msg("Invoking Worker Lambda asynchronously")

	_, err = lambdaClient.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(workerLambdaArn),
		InvocationType: lambdatypes.InvocationTypeEvent, // async — returns 202 immediately
		Payload:        payload,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to invoke Worker Lambda")
		return fmt.Errorf("invoke worker lambda: %w", err)
	}

	log.Debug().
		Str("type", fmt.Sprintf("%v", event["type"])).
		Str("jobId", fmt.Sprintf("%v", event["jobId"])).
		Msg("Worker Lambda invoked asynchronously")

	return nil
}
