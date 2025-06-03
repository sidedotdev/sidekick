package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// NewS3Client initializes and returns a new S3 client.
// It loads the default AWS configuration using the provided context.
func NewS3Client(ctx context.Context) (*s3.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// UploadJSONWithMetadata uploads a JSON byte slice to an S3 bucket with the specified key and metadata.
// The client parameter is the S3 client to use for the operation.
// The data parameter is the byte slice (expected to be JSON) to upload.
// The metadata parameter is a map of string key-value pairs for S3 object metadata.
// ContentType is set to "application/json".
func UploadJSONWithMetadata(ctx context.Context, client *s3.Client, bucket, key string, data []byte, metadata map[string]string) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		Metadata:    metadata,
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload to S3 bucket %s key %s: %w", bucket, key, err)
	}
	return nil
}

// DownloadObject downloads an object from an S3 bucket with the specified key and returns its content as a byte slice.
// The client parameter is the S3 client to use for the operation.
func DownloadObject(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download from S3 bucket %s key %s: %w", bucket, key, err)
	}
	defer output.Body.Close()

	bodyBytes, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object body from S3 bucket %s key %s: %w", bucket, key, err)
	}
	return bodyBytes, nil
}

// ListObjectKeys lists all object keys in an S3 bucket that match the given prefix.
// The client parameter is the S3 client to use for the operation.
// This function handles pagination to retrieve all matching keys.
func ListObjectKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects in S3 bucket %s with prefix %s: %w", bucket, prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
	}
	return keys, nil
}
