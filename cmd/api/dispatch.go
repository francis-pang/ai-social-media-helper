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

// invokeAsync sends an event to the specified Lambda function asynchronously (DDR-053).
// Uses InvocationType=Event so the API Lambda returns immediately without
// waiting for the target Lambda to process the job.
func invokeAsync(ctx context.Context, functionArn string, event map[string]interface{}) error {
	if lambdaClient == nil || functionArn == "" {
		log.Warn().Str("functionArn", functionArn).Msg("Lambda client not configured for async dispatch")
		return fmt.Errorf("lambda not configured: %s", functionArn)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal event")
		return fmt.Errorf("marshal event: %w", err)
	}

	log.Debug().Int("payloadSize", len(payload)).Str("functionArn", functionArn).Msg("Invoking Lambda asynchronously")

	_, err = lambdaClient.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName:   aws.String(functionArn),
		InvocationType: lambdatypes.InvocationTypeEvent, // async — returns 202 immediately
		Payload:        payload,
	})
	if err != nil {
		log.Error().Err(err).Str("functionArn", functionArn).Msg("Failed to invoke Lambda")
		return fmt.Errorf("invoke lambda: %w", err)
	}

	log.Debug().
		Str("type", fmt.Sprintf("%v", event["type"])).
		Str("jobId", fmt.Sprintf("%v", event["jobId"])).
		Str("functionArn", functionArn).
		Msg("Lambda invoked asynchronously")

	return nil
}
