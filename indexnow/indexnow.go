// Package indexnow pings the IndexNow API (Bing, Yandex, Seznam, Naver)
// so search engines recrawl changed content within minutes instead of
// waiting for the next scheduled crawl.
//
// Setup: generate a key (e.g. `uuidgen`), serve it as a plain-text file
// at https://<domain>/<key>.txt, and call SubmitSitemap whenever content
// changes. See https://www.indexnow.org/ for the protocol.
package indexnow

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// endpointBase is the IndexNow API root.
const endpointBase = "https://api.indexnow.org"

// SubmitSitemap asks IndexNow to recrawl the given sitemap URL. Submitting
// the sitemap is the recommended pattern: one ping covers every URL in it.
// Real HTTP failures (4xx/5xx) are returned as errors.
func SubmitSitemap(ctx context.Context, key, sitemapURL string) error {
	return submit(ctx, endpointBase, key, sitemapURL)
}

// submit performs the ping against a configurable base (for tests).
func submit(ctx context.Context, base, key, sitemapURL string) error {
	endpoint := base + "/indexnow?url=" + url.QueryEscape(sitemapURL) + "&key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("indexnow: %s", resp.Status)
	}
	return nil
}
