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
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
				if reqErr == nil {
					req.Header.Set("Content-Type", "application/json; charset=utf-8")
					resp, doErr := client.Do(req)
					if doErr == nil {
						if _, drainErr := io.Copy(io.Discard, resp.Body); drainErr != nil {
							doErr = drainErr
						}
						if closeErr := resp.Body.Close(); closeErr != nil && doErr == nil {
							doErr = closeErr
						}
						if doErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
							cancel()
							return
						}
						if doErr == nil {
							doErr = fmt.Errorf("HTTP %s", resp.Status)
						}
					}
					reqErr = doErr
				}
				cancel()
				if attempt == 1 {
					log.Warn("webhook: delivery failed", "host", host, "error", reqErr.Error())
				}
			}
		}()
	}, nil
}

func webhookLogHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}
