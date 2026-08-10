package storage

import (
	"crypto/tls"
	"io"
	"net/http"
)

// ioCopy is a thin alias so s3.go reads cleanly.
func ioCopy(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, src)
}

// httpNoVerifyClient skips TLS verification. Used ONLY when the operator
// explicitly opts in via S3_NO_VERIFY=1 (self-hosted MinIO over
// self-signed TLS); it is never the default.
type httpNoVerifyClient struct{}

func (c *httpNoVerifyClient) Do(req *http.Request) (*http.Response, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // operator-opted via env
	return tr.RoundTrip(req)
}
