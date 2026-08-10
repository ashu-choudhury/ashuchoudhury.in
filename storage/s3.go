package storage

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	Prefix   string // object prefix (e.g. "portfolio/")
	Key      string // default db object key (S3_PREFIX + db filename)
	Region   string
	Endpoint string
	Interval time.Duration
	NoVerify bool // S3_NO_VERIFY=1 disables TLS verification (self-hosted MinIO only)
}

// ConfigFromEnv builds the backup configuration from environment variables.
// The backup is disabled unless S3_BUCKET is set.
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
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	c.Prefix = prefix
	dbKey := envOr("S3_DB_KEY", "portfolio.db")
	c.Key = prefix + dbKey
	return c
}

// Backup uploads the storage/persisted directory to the bucket.
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
			o.HTTPClient = &httpNoVerifyClient{}
		}
	})
	return &Backup{cfg: cfg, client: client, log: logger}, nil
}

// Restore downloads missing files from S3 under storage/persisted/.
// Existing local data is not overwritten.
func (b *Backup) Restore(ctx context.Context, localPath string) error {
	rootDir := filepath.Dir(localPath)

	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.cfg.Bucket),
		Prefix: aws.String(b.cfg.Prefix),
	})

	restoredCount := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			b.log.Printf("s3: list objects under %s: %v", b.cfg.Prefix, err)
			break
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			relKey := strings.TrimPrefix(key, b.cfg.Prefix)
			if relKey == "" || strings.HasSuffix(relKey, "/") {
				continue
			}

			// Ignore temporary SQLite journal files
			if strings.HasSuffix(relKey, "-wal") || strings.HasSuffix(relKey, "-shm") || strings.HasSuffix(relKey, ".tmp") {
				continue
			}

			targetPath := filepath.Join(rootDir, filepath.FromSlash(relKey))

			// Skip if file already exists locally
			if _, err := os.Stat(targetPath); err == nil {
				continue
			}

			out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(b.cfg.Bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				b.log.Printf("s3: download %s failed: %v", key, err)
				continue
			}

			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				out.Body.Close()
				continue
			}

			f, err := os.Create(targetPath)
			if err != nil {
				out.Body.Close()
				continue
			}

			if _, err := ioCopy(f, out.Body); err != nil {
				_ = f.Close()
				out.Body.Close()
				continue
			}
			_ = f.Close()
			out.Body.Close()
			restoredCount++
		}
	}

	if restoredCount > 0 {
		b.log.Printf("s3: restored %d files into %s", restoredCount, rootDir)
	} else {
		b.log.Printf("s3: local storage synchronized (%s)", rootDir)
	}

	return nil
}

// RestoreFile fetches a single missing public file from S3 on-demand.
func (b *Backup) RestoreFile(ctx context.Context, relKey, targetPath string) error {
	s3Key := b.cfg.Prefix + "www/" + strings.TrimPrefix(filepath.ToSlash(relKey), "/")
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.cfg.Bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return err
	}
	defer out.Body.Close()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := ioCopy(f, out.Body); err != nil {
		return err
	}
	b.log.Printf("s3: lazy-restored missing file %s -> %s", s3Key, targetPath)
	return nil
}

// Backup checkpoints the database and uploads all files inside storage/persisted/ to S3.
func (b *Backup) Backup(ctx context.Context, checkpoint func(context.Context) error, localPath string) error {
	if checkpoint != nil {
		if err := checkpoint(ctx); err != nil {
			b.log.Printf("s3: wal checkpoint: %v", err)
		}
	}

	rootDir := filepath.Dir(localPath)
	if _, err := os.Stat(rootDir); os.IsNotExist(err) {
		return nil
	}

	uploadedCount := 0
	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		name := d.Name()
		// Ignore temporary SQLite journal files
		if strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, ".tmp") {
			return nil
		}

		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}

		objectKey := b.cfg.Prefix + filepath.ToSlash(relPath)

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:       aws.String(b.cfg.Bucket),
			Key:          aws.String(objectKey),
			Body:         f,
			ContentType:  aws.String(detectContentType(path)),
			StorageClass: "STANDARD",
		})
		if err != nil {
			b.log.Printf("s3: upload %s failed: %v", objectKey, err)
			return nil
		}

		uploadedCount++
		return nil
	})

	if err != nil {
		b.log.Printf("s3: directory walk %s: %v", rootDir, err)
	} else {
		b.log.Printf("s3: backed up %d files to %s", uploadedCount, b.cfg.Prefix)
	}

	return nil
}

func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".db":
		return "application/x-sqlite3"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".json":
		return "application/json"
	}
	return "application/octet-stream"
}

// Run loops backups on the configured interval until ctx is cancelled.
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
