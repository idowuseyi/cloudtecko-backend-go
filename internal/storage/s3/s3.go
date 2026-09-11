// Package s3 implements storage.Storage against any S3-compatible endpoint.
// Production targets Cloudflare R2; the same code works with AWS S3 or MinIO.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Storage is an S3-compatible object store.
type Storage struct {
	client        *minio.Client
	bucket        string
	publicBaseURL string
}

// Config carries what is needed to reach the store.
type Config struct {
	// Endpoint is the host[:port] of the S3 API, WITHOUT a scheme --
	// e.g. "<account>.r2.cloudflarestorage.com". Use UseSSL for the scheme.
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
	// PublicBaseURL is the domain public objects are served from. It should be a
	// CDN or bucket custom domain, NOT this API's own origin: serving
	// user-supplied files same-origin turns any stored-content flaw into a
	// same-origin scripting problem against the API.
	PublicBaseURL string
}

// New builds an S3-compatible store.
func New(config Config) (*Storage, error) {
	if config.Endpoint == "" || config.Bucket == "" {
		return nil, errors.New("storage: endpoint and bucket are required")
	}

	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseSSL,
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: failed to build client: %w", err)
	}

	return &Storage{
		client:        client,
		bucket:        config.Bucket,
		publicBaseURL: strings.TrimSuffix(config.PublicBaseURL, "/"),
	}, nil
}

func (s *Storage) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error) {
	// PresignedPutObject signs only the method, bucket, key and expiry. A
	// presigned POST policy could additionally bind content-length-range and
	// content-type, but R2's support for POST policies is inconsistent, so this
	// pipeline verifies after the fact instead.
	signed, err := s.client.PresignedPutObject(ctx, s.bucket, key, ttl)
	if err != nil {
		return "", fmt.Errorf("storage: failed to presign put: %w", err)
	}

	return signed.String(), nil
}

func (s *Storage) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return storage.ObjectInfo{}, storage.ErrObjectNotFound
		}

		return storage.ObjectInfo{}, fmt.Errorf("storage: failed to stat object: %w", err)
	}

	return storage.ObjectInfo{
		Key:         key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
	}, nil
}

func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, storage.ErrObjectNotFound
		}

		return nil, fmt.Errorf("storage: failed to get object: %w", err)
	}

	return object, nil
}

// Move copies then deletes. S3 has no atomic rename.
//
// If the DELETE fails after a successful COPY the destination exists and the
// source lingers; the caller sees an error and must treat the outcome as
// unknown. The orphan sweep over media_assets still in 'pending' is what
// eventually reclaims the stale source.
func (s *Storage) Move(ctx context.Context, from string, to string) error {
	if _, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: to},
		minio.CopySrcOptions{Bucket: s.bucket, Object: from},
	); err != nil {
		if isNotFound(err) {
			return storage.ErrObjectNotFound
		}

		return fmt.Errorf("storage: failed to copy object: %w", err)
	}

	if err := s.Delete(ctx, from); err != nil {
		return fmt.Errorf("storage: copied but failed to remove source: %w", err)
	}

	return nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: failed to remove object: %w", err)
	}

	return nil
}

func (s *Storage) PublicURL(key string) string {
	// Escape each segment individually: url.PathEscape would turn the separators
	// in "public/events/hero.jpg" into %2F, producing a single flat key that does
	// not resolve.
	segments := strings.Split(strings.TrimPrefix(key, "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}

	return s.publicBaseURL + "/" + strings.Join(segments, "/")
}

func isNotFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.StatusCode == http.StatusNotFound || resp.Code == "NoSuchKey"
	}

	return false
}
