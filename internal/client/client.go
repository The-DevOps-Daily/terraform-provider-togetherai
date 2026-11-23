// Package client is a small typed HTTP client for the parts of the Together AI
// REST API that the Terraform provider manages.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public Together AI API.
const DefaultBaseURL = "https://api.together.ai"

// Client calls the Together AI API with a Bearer API key.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client

	// KeyError, when set, is returned by every call instead of sending a
	// request. The provider uses it to report a missing API key only when a
	// resource or data source actually needs the API.
	KeyError error

	// MaxRetries is how many times a request is retried after a 429 or a 5xx.
	MaxRetries int
	// MinBackoff and MaxBackoff bound the wait between retries.
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

// New returns a client. An empty baseURL means DefaultBaseURL.
func New(apiKey, baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		userAgent:  userAgent,
		http:       &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 5,
		MinBackoff: time.Second,
		MaxBackoff: 30 * time.Second,
	}
}

// BaseURL returns the API base URL the client sends requests to.
func (c *Client) BaseURL() string { return c.baseURL }

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	// Type, Code, Param and Message come from the API's error body when it
	// has one ({"error": {...}} or {"code": ..., "message": ...}).
	Type    string
	Code    string
	Param   string
	Message string
	// Body is the raw response body (truncated) when it could not be parsed.
	Body string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Together AI API %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	switch {
	case e.Message != "":
		b.WriteString(": ")
		b.WriteString(e.Message)
		var extra []string
		if e.Type != "" {
			extra = append(extra, "type="+e.Type)
		}
		if e.Code != "" {
			extra = append(extra, "code="+e.Code)
		}
		if e.Param != "" {
			extra = append(extra, "param="+e.Param)
		}
		if len(extra) > 0 {
			b.WriteString(" (" + strings.Join(extra, ", ") + ")")
		}
	case e.Body != "":
		b.WriteString(": ")
		b.WriteString(e.Body)
	}
	return b.String()
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// StatusCode returns the HTTP status of an API error, or 0.
func StatusCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

const maxErrorBody = 2048

func parseAPIError(method, path string, status int, body []byte) *APIError {
	e := &APIError{StatusCode: status, Method: method, Path: path}

	// ErrorData: {"error": {"message", "type", "param", "code"}}. code can be
	// a string or a number depending on the service.
	var wrapped struct {
		Error *struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Param   string          `json:"param"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error != nil && wrapped.Error.Message != "" {
		e.Message = wrapped.Error.Message
		e.Type = wrapped.Error.Type
		e.Param = wrapped.Error.Param
		e.Code = rawToString(wrapped.Error.Code)
		return e
	}

	// ErrorResponse and gRPC gateway style: {"code": 3, "message": "..."}.
	var flat struct {
		Message string          `json:"message"`
		Error   string          `json:"error"`
		Code    json.RawMessage `json:"code"`
	}
	if json.Unmarshal(body, &flat) == nil && (flat.Message != "" || flat.Error != "") {
		e.Message = flat.Message
		if e.Message == "" {
			e.Message = flat.Error
		}
		e.Code = rawToString(flat.Code)
		return e
	}

	s := strings.TrimSpace(string(body))
	if len(s) > maxErrorBody {
		s = s[:maxErrorBody] + "..."
	}
	e.Body = s
	return e
}

func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// retryable reports whether a response status is worth retrying. POST is
// retried only on 429, because a 5xx after a create may mean the resource
// exists and a retry would create a second one.
func retryable(method string, status int, apiErr *APIError) bool {
	if status == http.StatusTooManyRequests {
		// A quota error (for example "GPU quota exceeded") will not clear by
		// waiting a few seconds.
		msg := strings.ToLower(apiErr.Message + " " + apiErr.Body)
		return !strings.Contains(msg, "quota")
	}
	if method == http.MethodPost {
		return false
	}
	return status >= 500 && status != http.StatusNotImplemented
}

func (c *Client) backoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > c.MaxBackoff {
				d = c.MaxBackoff
			}
			return d
		}
		if t, err := http.ParseTime(retryAfter); err == nil {
			d := time.Until(t)
			if d < 0 {
				d = 0
			}
			if d > c.MaxBackoff {
				d = c.MaxBackoff
			}
			return d
		}
	}
	d := c.MinBackoff << attempt
	if d <= 0 || d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	// Up to 20% jitter so parallel applies do not retry in lockstep.
	if d > 0 {
		d += time.Duration(rand.Int64N(int64(d)/5 + 1))
	}
	return d
}

// do sends a JSON request and decodes a JSON response into out (if not nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if c.KeyError != nil {
		return c.KeyError
	}

	var payload []byte
	if in != nil {
		var err error
		payload, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}

		res, err := c.http.Do(req)
		if err != nil {
			// Network errors on idempotent requests are retried; a POST may
			// have reached the server, so it is not.
			if method != http.MethodPost && attempt < c.MaxRetries && ctx.Err() == nil {
				if werr := sleep(ctx, c.backoff(attempt, "")); werr != nil {
					return werr
				}
				continue
			}
			return fmt.Errorf("calling the Together AI API %s %s: %w", method, path, err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("reading Together AI API response for %s %s: %w", method, path, readErr)
		}

		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decoding Together AI API response for %s %s: %w", method, path, err)
			}
			return nil
		}

		apiErr := parseAPIError(method, path, res.StatusCode, respBody)
		c.redact(apiErr)
		if attempt < c.MaxRetries && retryable(method, res.StatusCode, apiErr) {
			if werr := sleep(ctx, c.backoff(attempt, res.Header.Get("Retry-After"))); werr != nil {
				return werr
			}
			continue
		}
		return apiErr
	}
}

// redact removes the API key from error text the API or a proxy echoed back,
// so it cannot reach Terraform output or CI logs.
func (c *Client) redact(e *APIError) {
	if c.apiKey == "" {
		return
	}
	for _, s := range []*string{&e.Message, &e.Body, &e.Param, &e.Code, &e.Type} {
		*s = strings.ReplaceAll(*s, c.apiKey, "<redacted>")
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func pathEscape(s string) string { return url.PathEscape(s) }
