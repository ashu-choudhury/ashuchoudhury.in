package storage

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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
	b := &Backup{cfg: cfg, client: client, log: logger}
	mountLinuxS3FS(cfg, logger)
	return b, nil
}

// mountLinuxS3FS mounts the S3 bucket's www folder into storage/persisted/www on Linux using s3fs if available.
func mountLinuxS3FS(cfg Config, logger *log.Logger) {
	wwwDir := filepath.Join("storage", "persisted", "www")
	_ = os.MkdirAll(wwwDir, 0755)

	if runtime.GOOS != "linux" {
		logger.Printf("s3: www mount check (OS: %s)", runtime.GOOS)
		return
	}

	s3fsBin, err := exec.LookPath("s3fs")
	if err != nil {
		logger.Printf("s3: s3fs not found in PATH — skipping auto-mount")
		return
	}

	bucketTarget := cfg.Bucket + ":" + strings.TrimSuffix(cfg.Prefix, "/") + "/www"
	args := []string{
		bucketTarget,
		wwwDir,
		"-o", "use_path_style",
		"-o", "allow_other",
	}
	if cfg.Endpoint != "" {
		args = append(args, "-o", "url="+cfg.Endpoint)
	}

	cmd := exec.Command(s3fsBin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		logger.Printf("s3: s3fs mount: %v (out: %s)", err, strings.TrimSpace(string(out)))
	} else {
		logger.Printf("s3: mounted %s to %s via s3fs", bucketTarget, wwwDir)
	}
}

// Restore downloads missing files from S3 under storage/persisted/ in parallel.
// Existing local data is not overwritten if size and modtime match.
func (b *Backup) Restore(ctx context.Context, localPath string) error {
	rootDir := filepath.Dir(localPath)

	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.cfg.Bucket),
		Prefix: aws.String(b.cfg.Prefix),
	})

	type restoreTask struct {
		key        string
		targetPath string
		modTime    *time.Time
	}

	var tasks []restoreTask
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

			// Mandate: www/ is mounted directly via Linux S3FS / network mount
			if strings.HasPrefix(relKey, "www/") || relKey == "www" {
				continue
			}

			targetPath := filepath.Join(rootDir, filepath.FromSlash(relKey))

			// Skip if file exists locally with identical size and S3 object is not newer
			if st, err := os.Stat(targetPath); err == nil {
				objSize := aws.ToInt64(obj.Size)
				if st.Size() == objSize && (obj.LastModified == nil || !st.ModTime().Before(*obj.LastModified)) {
					continue
				}
			}

			tasks = append(tasks, restoreTask{
				key:        key,
				targetPath: targetPath,
				modTime:    obj.LastModified,
			})
		}
	}

	if len(tasks) == 0 {
		b.log.Printf("s3: local storage synchronized (%s)", rootDir)
		return nil
	}

	workers := 8
	if len(tasks) < workers {
		workers = len(tasks)
	}

	taskCh := make(chan restoreTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var restoredCount int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				body, err := b.getObjectReader(ctx, t.key)
				if err != nil {
					b.log.Printf("s3: download %s failed: %v", t.key, err)
					continue
				}

				if err := os.MkdirAll(filepath.Dir(t.targetPath), 0755); err != nil {
					body.Close()
					continue
				}

				f, err := os.Create(t.targetPath)
				if err != nil {
					body.Close()
					continue
				}

				if _, err := ioCopy(f, body); err != nil {
					_ = f.Close()
					body.Close()
					continue
				}
				_ = f.Close()
				body.Close()

				if t.modTime != nil {
					_ = os.Chtimes(t.targetPath, *t.modTime, *t.modTime)
				}
				atomic.AddInt64(&restoredCount, 1)
			}
		}()
	}
	wg.Wait()

	if restoredCount > 0 {
		b.log.Printf("s3: parallel-restored %d files into %s", restoredCount, rootDir)
	} else {
		b.log.Printf("s3: local storage synchronized (%s)", rootDir)
	}

	return nil
}

// RestoreFile fetches a single missing public file from S3 on-demand.
func (b *Backup) RestoreFile(ctx context.Context, relKey, targetPath string) error {
	cleanRel := strings.TrimPrefix(filepath.ToSlash(relKey), "/")
	if unescaped, err := url.PathUnescape(cleanRel); err == nil && unescaped != "" {
		cleanRel = unescaped
	}

	s3Key := b.cfg.Prefix + "www/" + cleanRel
	body, err := b.getObjectReader(ctx, s3Key)
	if err != nil {
		rawS3Key := b.cfg.Prefix + "www/" + strings.TrimPrefix(filepath.ToSlash(relKey), "/")
		if rawS3Key != s3Key {
			body, err = b.getObjectReader(ctx, rawS3Key)
		}
		if err != nil {
			b.log.Printf("s3: restore file failed for %s (%s): %v", relKey, s3Key, err)
			return err
		}
	}
	defer body.Close()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := ioCopy(f, body); err != nil {
		return err
	}
	b.log.Printf("s3: lazy-restored missing file %s -> %s", s3Key, targetPath)
	return nil
}

// getObjectReader fetches an object stream from S3. Handles 302 Found redirects automatically
// for providers like Hugging Face S3 (s3.hf.co).
func (b *Backup) getObjectReader(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return out.Body, nil
	}

	// Extract presigned 302 Location URL header from AWS SDK error
	loc := getRedirectLocation(err)
	if loc != "" {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
		if reqErr == nil {
			client := &http.Client{Timeout: 120 * time.Second}
			resp, respErr := client.Do(req)
			if respErr == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
				b.log.Printf("s3: followed 302 Location redirect for %s", key)
				return resp.Body, nil
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
	}

	// Fallback manual URL formatting if Location header was not in error response
	if strings.Contains(err.Error(), "302") || strings.Contains(err.Error(), "Found") {
		base := strings.TrimSuffix(b.cfg.Endpoint, "/")
		bucket := b.cfg.Bucket
		cleanKey := strings.TrimPrefix(key, "/")

		var reqURL string
		if base != "" {
			if strings.HasSuffix(base, "/"+bucket) {
				reqURL = base + "/" + cleanKey
			} else {
				reqURL = base + "/" + bucket + "/" + cleanKey
			}
		} else {
			reqURL = "https://" + bucket + ".s3." + b.cfg.Region + ".amazonaws.com/" + cleanKey
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if reqErr == nil {
			client := &http.Client{Timeout: 120 * time.Second}
			resp, respErr := client.Do(req)
			if respErr == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
				b.log.Printf("s3: followed manual 302 redirect for %s", key)
				return resp.Body, nil
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
	}

	return nil, err
}

func getRedirectLocation(err error) string {
	if err == nil {
		return ""
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.Response != nil {
		if loc := respErr.Response.Header.Get("Location"); loc != "" {
			return loc
		}
	}
	errStr := err.Error()
	if idx := strings.Index(errStr, "http://"); idx != -1 {
		return strings.Fields(errStr[idx:])[0]
	}
	if idx := strings.Index(errStr, "https://"); idx != -1 {
		return strings.Fields(errStr[idx:])[0]
	}
	return ""
}

// Backup checkpoints the database and uploads all files inside storage/persisted/ to S3 in parallel.
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

	type uploadTask struct {
		path      string
		objectKey string
	}

	var tasks []uploadTask
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

		// Mandate: www/ is mounted directly via Linux S3FS / network mount
		if strings.HasPrefix(relPath, "www/") || strings.HasPrefix(relPath, "www\\") || relPath == "www" {
			return nil
		}

		objectKey := b.cfg.Prefix + filepath.ToSlash(relPath)
		tasks = append(tasks, uploadTask{path: path, objectKey: objectKey})
		return nil
	})

	if err != nil {
		b.log.Printf("s3: directory walk %s: %v", rootDir, err)
		return nil
	}

	if len(tasks) == 0 {
		return nil
	}

	workers := 8
	if len(tasks) < workers {
		workers = len(tasks)
	}

	taskCh := make(chan uploadTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var uploadedCount int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				f, err := os.Open(t.path)
				if err != nil {
					continue
				}

				_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
					Bucket:       aws.String(b.cfg.Bucket),
					Key:          aws.String(t.objectKey),
					Body:         f,
					ContentType:  aws.String(detectContentType(t.path)),
					StorageClass: "STANDARD",
				})
				_ = f.Close()

				if err != nil {
					b.log.Printf("s3: upload %s failed: %v", t.objectKey, err)
					continue
				}

				atomic.AddInt64(&uploadedCount, 1)
			}
		}()
	}
	wg.Wait()

	b.log.Printf("s3: parallel backed up %d files to %s", uploadedCount, b.cfg.Prefix)
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

// Run loops backups and periodic S3 restores on interval until ctx is cancelled.
func (b *Backup) Run(ctx context.Context, checkpoint func(context.Context) error, localPath string) {
	backupTicker := time.NewTicker(b.cfg.Interval)
	defer backupTicker.Stop()

	// Periodic 60-second S3 restore ticker so Vercel continuously receives Render S3 updates
	restoreTicker := time.NewTicker(60 * time.Second)
	defer restoreTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.log.Printf("s3: final backup on shutdown")
			_ = b.Backup(context.Background(), checkpoint, localPath)
			return
		case <-restoreTicker.C:
			_ = b.Restore(ctx, localPath)
		case <-backupTicker.C:
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
