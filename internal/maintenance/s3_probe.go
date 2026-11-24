package maintenance

import (
	"context"
	"time"

	"household/internal/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func testS3Bucket(ctx context.Context, target config.S3Connection) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := minio.New(target.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(target.AccessKey, target.SecretKey, ""),
		Secure: target.Secure, Region: target.Region, BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return &ToolError{Program: "s3", Code: "repository_unreachable", err: err}
	}
	exists, err := client.BucketExists(ctx, target.Bucket)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		response := minio.ToErrorResponse(err)
		switch {
		case response.StatusCode == 403 || response.StatusCode == 401,
			response.Code == "AccessDenied", response.Code == "InvalidAccessKeyId", response.Code == "SignatureDoesNotMatch":
			return &ToolError{Program: "s3", Code: "access_denied", err: err}
		case response.StatusCode == 404 || response.Code == "NoSuchBucket":
			return &ToolError{Program: "s3", Code: "bucket_missing", err: err}
		default:
			return &ToolError{Program: "s3", Code: "repository_unreachable", err: err}
		}
	}
	if !exists {
		return &ToolError{Program: "s3", Code: "bucket_missing"}
	}
	return nil
}
