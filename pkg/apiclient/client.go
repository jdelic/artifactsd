// Package apiclient is the Go client for the artifactsd management API, shared
// by the CLI and MCP adapter (design/01 §4). It owns request construction, the
// bearer-token auth header, and decoding of the API's JSON error envelope
// (design/04 §1) into a typed Error, so callers handle one error shape.
package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds a single API request.
const DefaultTimeout = 60 * time.Second

// Config configures a Client.
type Config struct {
	BaseURL  string // e.g. https://artifacts.example.com
	Token    string // management API token (tok_...)
	Insecure bool   // skip TLS verification (dev only)
	// HTTP overrides the underlying client (mainly for tests). When nil a client
	// honoring Insecure and DefaultTimeout is built.
	HTTP *http.Client
}

// Client talks to one artifactsd server's management API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a Client from cfg. The base URL is normalized (trailing slash
// trimmed).
func New(cfg Config) *Client {
	hc := cfg.HTTP
	if hc == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.Insecure {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in dev-only flag
		}
		hc = &http.Client{Timeout: DefaultTimeout, Transport: transport}
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.Token,
		http:    hc,
	}
}

// Error is a typed API failure carrying the HTTP status and the server's error
// envelope code/message (design/04 §1).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("request failed: HTTP %d", e.Status)
}

// IsNotFound reports whether err is an API 404.
func IsNotFound(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// IsUnauthorized reports whether err is an API 401/403.
func IsUnauthorized(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden)
}

// IsConflict reports whether err is an API 409.
func IsConflict(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict
}

// errorEnvelope mirrors the API error body.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// sendJSON sends a JSON request (body may be nil) and decodes a JSON response
// into out (out may be nil to discard). Non-2xx responses become a typed *Error.
func (c *Client) sendJSON(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("apiclient: encoding request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := c.newRequest(ctx, method, path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apiclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("apiclient: decoding response: %w", err)
	}
	return nil
}

// sendRaw sends a request with a raw (non-JSON) body and the given headers,
// decoding a JSON response into out (out may be nil). Used for binary uploads.
func (c *Client) sendRaw(ctx context.Context, method, path string, body io.Reader, headers map[string]string, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apiclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("apiclient: decoding response: %w", err)
	}
	return nil
}

// openStream issues a GET and returns the response body for streaming. The
// caller must Close it. Non-2xx responses become a typed *Error.
func (c *Client) openStream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apiclient: GET %s: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}

// newRequest builds an authenticated request against the client's base URL.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("apiclient: building request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

// decodeError maps a non-2xx response to a typed *Error, falling back to the
// status when the body is not the standard envelope.
func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	apiErr := &Error{Status: resp.StatusCode}
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
		apiErr.Code = env.Error.Code
		apiErr.Message = env.Error.Message
	} else {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	return apiErr
}
