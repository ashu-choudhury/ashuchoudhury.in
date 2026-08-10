// Package storage provides durable persistence for the site's data
// directory. Everything is configured from the environment — nothing is
// hardcoded — so the repository can be published publicly without secrets.
//
// Currently it implements an S3-compatible backup/restore for the SQLite
// database (AWS S3, Cloudflare R2, MinIO, and similar all work via
// S3_ENDPOINT + path-style requests).
package storage

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config carries the S3 backup settings, populated from the environment.
type Config struct {
	Enabled  bool
	Bucket   string
	Key      string // object key (S3_PREFIX + db filename)
	Region   string
	Endpoint string
	Interval time.Duration
	NoVerify bool // S3_NO_VERIFY=1 disables TLS verification (self-hosted MinIO only)
}

// ConfigFromEnv builds the backup configuration from environment variables.
// The backup is disabled unless S3_BUCKET is set.
//
//	S3_BUCKET                required — bucket name (enables the backup)
//	S3_REGION                default us-east-1
//	S3_ENDPOINT              optional — custom endpoint (R2 / MinIO)
//	S3_PREFIX                optional — object key prefix, default "portfolio/"
//	S3_DB_KEY                optional — db object name, default "portfolio.db"
//	S3_INTERVAL              seconds between backups, default 300
//	AWS_ACCESS_KEY_ID        optional — falls back to the default credential chain
//	AWS_SECRET_ACCESS_KEY    optional
//	AWS_SESSION_TOKEN        optional
func ConfigFromEnv() Config {
	c := Config{
		Bucket:   os.Getenv("S3_BUCKET"),
		Region:   envOr("S3_REGION", "us-east-1"),
		Endpoint: os.Getenv("S3_ENDPOINT"),
		Interval: time.Duration(envInt("S3_INTERVAL", 300)) * time.Second,
		NoVerify: os.Getenv("S3_NO_VERIFY") == "1" || os.Getenv("S3_NO_VERIFY") == "true",
	}
	if c.Bucket == "" {
		return c // disabled
	}
	c.Enabled = true
	prefix := envOr("S3_PREFIX", "portfolio/")
	dbKey := envOr("S3_DB_KEY", "portfolio.db")
	c.Key = prefix + dbKey
	return c
}

// Backup uploads the SQLite database to the bucket. The database is
// checkpointed first (WAL flushed) so the object is a consistent snapshot.
type Backup struct {
	cfg    Config
	client *s3.Client
	log    *log.Logger
}

// NewBackup builds the S3 client from cfg. Call only when cfg.Enabled.
func NewBackup(cfg Config, logger *log.Logger) (*Backup, error) {
	if logger == nil {
		logger = log.Default()
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"); id != "" && secret != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			id, secret, os.Getenv("AWS_SESSION_TOKEN"),
		)))
	}
	cfgAWS, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfgAWS, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
		if cfg.NoVerify {
			// Self-hosted MinIO over plain HTTP / self-signed TLS.
			o.HTTPClient = &httpNoVerifyClient{}
		}
	})
	return &Backup{cfg: cfg, client: client, log: logger}, nil
}

// Restore downloads the database from S3 when the local file is missing
// (e.g. a brand-new container with an empty volume). Existing local data is
// never overwritten — the site is the source of truth once it has run.
func (b *Backup) Restore(ctx context.Context, localPath string) error {
	if _, err := os.Stat(localPath); err == nil {
		b.log.Printf("s3: local database present, skipping restore (%s)", localPath)
		return nil
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.cfg.Bucket),
		Key:    aws.String(b.cfg.Key),
	})
	if err != nil {
		b.log.Printf("s3: no backup found at %s/%s (%v) — starting fresh", b.cfg.Bucket, b.cfg.Key, err)
		return nil // no backup yet; first run
	}
	defer out.Body.Close()
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	if _, err := ioCopy(f, out.Body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	b.log.Printf("s3: restored database from %s/%s", b.cfg.Bucket, b.cfg.Key)
	return nil
}

// Backup checkpoints the database and uploads it to S3.
func (b *Backup) Backup(ctx context.Context, checkpoint func(context.Context) error, localPath string) error {
	if checkpoint != nil {
		if err := checkpoint(ctx); err != nil {
			b.log.Printf("s3: wal checkpoint: %v", err)
		}
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(b.cfg.Bucket),
		Key:          aws.String(b.cfg.Key),
		Body:         f,
		ContentType:  aws.String("application/octet-stream"),
		StorageClass: "STANDARD",
	})
	if err != nil {
		return err
	}
	b.log.Printf("s3: backed up database to %s/%s", b.cfg.Bucket, b.cfg.Key)
	return nil
}

// Run loops backups on the configured interval until ctx is cancelled.
// The caller should cancel ctx (and wait) on shutdown so a final backup
// happens after the last write.
func (b *Backup) Run(ctx context.Context, checkpoint func(context.Context) error, localPath string) {
	ticker := time.NewTicker(b.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			b.log.Printf("s3: final backup on shutdown")
			_ = b.Backup(context.Background(), checkpoint, localPath)
			return
		case <-ticker.C:
			if err := b.Backup(ctx, checkpoint, localPath); err != nil {
				b.log.Printf("s3: backup failed: %v", err)
			}
		}
	}
}

// envOr returns def when the variable is unset or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt parses an integer environment variable with a default.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
