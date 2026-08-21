package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewWebhookNotifier creates an asynchronous best-effort task notifier.
// Empty URL is disabled and returns nil.
func NewWebhookNotifier(url string, timeout time.Duration, log *slog.Logger) (func(TaskFinishSnapshot), error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, nil
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("webhook: timeout must be positive")
	}
	if log == nil {
		log = slog.Default()
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	host := webhookLogHost(url)
	return func(snapshot TaskFinishSnapshot) {
		// Доставка best-effort: detached goroutine может быть прервана shutdown'ом.
		go func() {
			body, err := json.Marshal(snapshot)
			if err != nil {
				log.Warn("webhook: encode failed", "error", err.Error())
				return
			}
			for attempt := 0; attempt < 2; attempt++ {
				reqErr := postOnce(client, url, body, timeout)
				if reqErr == nil {
					return
				}
				if attempt == 1 {
					log.Warn("webhook: delivery failed", "host", host, "error", reqErr.Error())
				}
			}
		}()
	}, nil
}

func postOnce(client *http.Client, rawURL string, body []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, drainErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if drainErr != nil {
		return drainErr
	}
	if closeErr != nil {
		return closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

func webhookLogHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}
