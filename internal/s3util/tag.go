package s3util

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// projectTag is the URL-encoded S3 object tagging string for cost allocation (DDR-049).
const projectTag = "Project=ai-social-media-helper"

// ProjectTagging returns a pointer to the URL-encoded S3 object tagging string.
// Use as the Tagging field on PutObjectInput and CreateMultipartUploadInput.
func ProjectTagging() *string {
	t := projectTag
	return &t
}

// TagObject applies the Project cost-allocation tag to an existing S3 object (DDR-049).
// Used for browser-uploaded files that cannot be tagged at creation time (presigned URLs).
func TagObject(ctx context.Context, client *s3.Client, bucket, key string) error {
	_, err := client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: &bucket,
		Key:    &key,
		Tagging: &s3types.Tagging{
			TagSet: []s3types.Tag{
				{Key: aws.String("Project"), Value: aws.String("ai-social-media-helper")},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("PutObjectTagging: %w", err)
	}
	return nil
}
