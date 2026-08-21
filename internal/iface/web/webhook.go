package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	return func(snapshot TaskFinishSnapshot) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			body, err := json.Marshal(snapshot)
			if err != nil {
				log.Warn("webhook: encode failed", "error", err.Error())
				return
			}
			for attempt := 0; attempt < 2; attempt++ {
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
				if reqErr == nil {
					req.Header.Set("Content-Type", "application/json; charset=utf-8")
					resp, doErr := client.Do(req)
					if doErr == nil {
						_ = resp.Body.Close()
						if resp.StatusCode >= 200 && resp.StatusCode < 300 {
							return
						}
						doErr = fmt.Errorf("HTTP %s", resp.Status)
					}
					reqErr = doErr
				}
				if attempt == 1 {
					log.Warn("webhook: delivery failed", "url", url, "error", reqErr.Error())
				}
				if ctx.Err() != nil {
					return
				}
			}
		}()
	}, nil
}
