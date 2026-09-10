package storage

import (
	"context"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 is an S3-compatible ObjectStore (MinIO locally, Cloudflare R2 in
// production). Keys live in a single bucket; variants are stored next to the
// original under server-generated keys.
type S3 struct {
	client     *s3.Client
	presign    *s3.PresignClient
	bucket     string
	publicBase string
}

func NewS3(endpoint, region, bucket, accessKey, secretKey, publicBase string) *S3 {
	cfg := aws.Config{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		BaseEndpoint: aws.String(endpoint),
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// MinIO and R2 both support path-style addressing.
		o.UsePathStyle = true
	})
	return &S3{
		client:     client,
		presign:    s3.NewPresignClient(client),
		bucket:     bucket,
		publicBase: strings.TrimSuffix(publicBase, "/"),
	}
}

func (s *S3) Bucket() string { return s.bucket }

func (s *S3) PresignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (s *S3) PublicReadURL(key string) string {
	return s.publicBase + "/media/objects/" + url.PathEscape(key)
}

func (s *S3) Upload(ctx context.Context, key string, r io.Reader, contentType string, sizeBytes int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(sizeBytes),
	})
	return err
}

func (s *S3) Download(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, err
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return out.Body, size, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}
