package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ccLoad/internal/site/provider"
)

const maxWebhookResponseBytes int64 = 64 << 10

type Sender struct {
	Clients provider.ClientFactory
	Timeout time.Duration
}

// Send posts a JSON event and retries only transient network, rate-limit, and
// upstream failures. It never includes the endpoint URL in returned errors.
func (s Sender) Send(ctx context.Context, endpoint string, payload any) (int, error) {
	if err := provider.ValidateBaseURL(endpoint, s.Clients.AllowPrivate); err != nil {
		return 0, errors.New("invalid webhook endpoint")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, errors.New("encode webhook payload")
	}
	client, err := s.Clients.New("")
	if err != nil {
		return 0, errors.New("create webhook client")
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		req, requestErr := http.NewRequestWithContext(attemptCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			cancel()
			return attempt, errors.New("create webhook request")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "PivotFlow-Webhook/1")
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookResponseBytes))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				cancel()
				return attempt, nil
			}
			lastErr = errors.New("webhook returned HTTP " + strconv.Itoa(resp.StatusCode))
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				cancel()
				return attempt, lastErr
			}
		} else if errors.Is(requestErr, context.Canceled) && ctx.Err() != nil {
			cancel()
			return attempt, ctx.Err()
		} else {
			lastErr = errors.New("webhook request failed")
			if strings.Contains(strings.ToLower(requestErr.Error()), "certificate") {
				cancel()
				return attempt, lastErr
			}
		}
		cancel()
		if attempt < 3 {
			delay := time.Duration(attempt) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return attempt, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return 3, lastErr
}
