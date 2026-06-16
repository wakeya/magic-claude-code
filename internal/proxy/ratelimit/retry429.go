package ratelimit

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

type errorCode429 int

const (
	codeUnknown errorCode429 = 0
	code1302    errorCode429 = 1302
	code1305    errorCode429 = 1305
)

type retry429Config struct {
	maxAttempts    int
	initialDelay   time.Duration
	maxDelay       time.Duration
}

func parseRetryAfter(resp *http.Response, maxDelay time.Duration) time.Duration {
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(raw); err == nil {
		d := time.Duration(seconds) * time.Second
		return capDelay(d, maxDelay)
	}

	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return capDelay(d, maxDelay)
	}

	return 0
}

func capDelay(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}

func parse429Body(body []byte) (errorCode429, string) {
	var errResp struct {
		Error struct {
			Type string `json:"type"`
			Code int    `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		return codeUnknown, ""
	}

	msg := errResp.Message
	if msg == "" {
		msg = errResp.Error.Type
	}

	switch errResp.Error.Code {
	case 1302:
		return code1302, msg
	case 1305:
		return code1305, msg
	default:
		return codeUnknown, msg
	}
}

func computeBackoff(cfg retry429Config, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	multiplier := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(cfg.initialDelay) * multiplier)
	jitter := time.Duration(rand.Intn(250)) * time.Millisecond
	delay += jitter
	return capDelay(delay, cfg.maxDelay)
}

type doResult struct {
	resp *http.Response
	err  error
}

func retry429(
	ctx context.Context,
	doRequest func() (*http.Response, error),
	cfg retry429Config,
	logf func(format string, args ...any),
) (*http.Response, error) {
	resp, err := doRequest()
	if err != nil {
		return resp, err
	}

	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}

	for attempt := 0; attempt < cfg.maxAttempts; attempt++ {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		resp.Body.Close()

		code, msg := parse429Body(bodyBytes)
		delay := parseRetryAfter(resp, cfg.maxDelay)
		if delay == 0 {
			delay = computeBackoff(cfg, attempt)
		}

		if logf != nil {
			if code != codeUnknown {
				logf("[Retry429] attempt %d/%d, code=%d, msg=%s, waiting %v", attempt+1, cfg.maxAttempts, code, msg, delay)
			} else {
				logf("[Retry429] attempt %d/%d, waiting %v", attempt+1, cfg.maxAttempts, delay)
			}
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		resp, err = doRequest()
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
	}

	return resp, nil
}

func Retry429ConfigFromProvider(enabled bool, maxAttempts, initialDelayMS, maxDelayMS int) (retry429Config, bool) {
	if !enabled || maxAttempts <= 0 {
		return retry429Config{}, false
	}
	return retry429Config{
		maxAttempts:  maxAttempts,
		initialDelay: time.Duration(initialDelayMS) * time.Millisecond,
		maxDelay:     time.Duration(maxDelayMS) * time.Millisecond,
	}, true
}

func DoWithRetry429(
	ctx context.Context,
	doRequest func() (*http.Response, error),
	enabled bool,
	maxAttempts, initialDelayMS, maxDelayMS int,
	logf func(string, ...any),
) (*http.Response, error) {
	if !enabled {
		return doRequest()
	}
	cfg, ok := Retry429ConfigFromProvider(enabled, maxAttempts, initialDelayMS, maxDelayMS)
	if !ok {
		return doRequest()
	}
	return retry429(ctx, doRequest, cfg, logf)
}
