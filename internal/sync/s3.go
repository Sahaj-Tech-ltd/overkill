package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// S3Backend speaks S3 through minio-go, which works against AWS S3, R2, B2,
// MinIO and any other S3-compatible store.
type S3Backend struct {
	cli    *minio.Client
	bucket string
	prefix string
}

func NewS3Backend(cfg config.SyncS3Config) (*S3Backend, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("sync/s3: bucket is required")
	}
	endpoint := cfg.Endpoint
	useSSL := cfg.UseSSL
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
		useSSL = true
	}
	// minio-go expects bare host:port, but users frequently configure full URLs.
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
		useSSL = u.Scheme == "https"
	}

	cli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("sync/s3: client: %w", err)
	}
	return &S3Backend{cli: cli, bucket: cfg.Bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

func (s *S3Backend) Name() string { return "s3" }

func (s *S3Backend) key(suffix string) string {
	if s.prefix == "" {
		return suffix
	}
	return s.prefix + "/" + suffix
}

func (s *S3Backend) blobKey(id string) string { return s.key(id + ".json.gz") }
func (s *S3Backend) metaKey(id string) string { return s.key(id + ".meta.json") }

func (s *S3Backend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	r := bytes.NewReader(data)
	if _, err := s.cli.PutObject(ctx, s.bucket, s.blobKey(id), r, int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return fmt.Errorf("sync/s3: put blob: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sync/s3: marshal meta: %w", err)
	}
	if _, err := s.cli.PutObject(ctx, s.bucket, s.metaKey(id), bytes.NewReader(mb), int64(len(mb)),
		minio.PutObjectOptions{ContentType: "application/json"}); err != nil {
		return fmt.Errorf("sync/s3: put meta: %w", err)
	}
	return nil
}

func (s *S3Backend) Pull(ctx context.Context, id string) ([]byte, SessionMeta, error) {
	obj, err := s.cli.GetObject(ctx, s.bucket, s.blobKey(id), minio.GetObjectOptions{})
	if err != nil {
		return nil, SessionMeta{}, fmt.Errorf("sync/s3: get blob: %w", err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		var e minio.ErrorResponse
		if errors.As(err, &e) && (e.Code == "NoSuchKey" || e.StatusCode == 404) {
			return nil, SessionMeta{}, ErrNotFound
		}
		return nil, SessionMeta{}, fmt.Errorf("sync/s3: read blob: %w", err)
	}
	if len(data) == 0 {
		return nil, SessionMeta{}, ErrNotFound
	}

	var meta SessionMeta
	if mObj, err := s.cli.GetObject(ctx, s.bucket, s.metaKey(id), minio.GetObjectOptions{}); err == nil {
		if mb, err := io.ReadAll(mObj); err == nil {
			_ = json.Unmarshal(mb, &meta)
		}
		mObj.Close()
	}
	if meta.ID == "" {
		meta.ID = id
	}
	return data, meta, nil
}

func (s *S3Backend) List(ctx context.Context) ([]SessionMeta, error) {
	prefix := s.prefix
	if prefix != "" {
		prefix += "/"
	}
	var out []SessionMeta
	for obj := range s.cli.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("sync/s3: list: %w", obj.Err)
		}
		if !strings.HasSuffix(obj.Key, ".meta.json") {
			continue
		}
		mObj, err := s.cli.GetObject(ctx, s.bucket, obj.Key, minio.GetObjectOptions{})
		if err != nil {
			continue
		}
		mb, _ := io.ReadAll(mObj)
		mObj.Close()
		var meta SessionMeta
		if err := json.Unmarshal(mb, &meta); err == nil {
			out = append(out, meta)
		}
	}
	return out, nil
}

func (s *S3Backend) Delete(ctx context.Context, id string) error {
	_ = s.cli.RemoveObject(ctx, s.bucket, s.blobKey(id), minio.RemoveObjectOptions{})
	_ = s.cli.RemoveObject(ctx, s.bucket, s.metaKey(id), minio.RemoveObjectOptions{})
	return nil
}
