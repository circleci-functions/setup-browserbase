// Package browserbase is a small client for the Browserbase sessions API.
package browserbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIURL is the Browserbase API.
const DefaultAPIURL = "https://api.browserbase.com"

// ReplayURL returns where a session's replay is viewed.
func ReplayURL(id string) string {
	return "https://browserbase.com/sessions/" + id
}

// Session is the subset of a Browserbase session setup-browserbase reads.
type Session struct {
	ID         string `json:"id"`
	ConnectURL string `json:"connectUrl"`
	Status     string `json:"status"`
}

// CreateOptions configures a new session.
type CreateOptions struct {
	ProjectID string
	// Timeout ends the session even if nothing releases it.
	Timeout time.Duration
	// Metadata tags the session, so the job can find its own sessions later.
	Metadata map[string]any
}

// APIError is a non-2xx answer from the Browserbase API.
type APIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("browserbase API: %s (%d)", msg, e.Status)
}

// LimitError is a 429 that did not clear before the deadline.
type LimitError struct{ Err *APIError }

func (e *LimitError) Error() string {
	return "Browserbase refused the session: the plan's limit on concurrent sessions or new sessions a minute is reached " +
		"(Free: 3 concurrent, 5 a minute; Developer: 25 and 25). Lower `sessions`, or use a project only CI shares: " + e.Err.Error()
}

func (e *LimitError) Unwrap() error { return e.Err }

// Client calls the Browserbase API with one key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	// Wait is how long to keep retrying a 429.
	Wait time.Duration
}

// New returns a client for baseURL.
func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
		Wait:    2 * time.Minute,
	}
}

// Create starts a session, waiting out plan limits for up to c.Wait.
func (c *Client) Create(ctx context.Context, opts CreateOptions) (Session, error) {
	body := map[string]any{}
	if opts.ProjectID != "" {
		body["projectId"] = opts.ProjectID
	}
	if opts.Timeout > 0 {
		body["timeout"] = int(opts.Timeout.Seconds())
	}
	if len(opts.Metadata) > 0 {
		body["userMetadata"] = opts.Metadata
	}
	var s Session
	err := c.retry(ctx, func() error { return c.do(ctx, http.MethodPost, "/v1/sessions", body, &s) })
	return s, err
}

// Release ends a session before its timeout. Ending one that already ended is
// not an error.
func (c *Client) Release(ctx context.Context, id string, projectID string) error {
	body := map[string]any{"status": "REQUEST_RELEASE"}
	if projectID != "" {
		body["projectId"] = projectID
	}
	err := c.retry(ctx, func() error { return c.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id), body, nil) })
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusConflict) {
		return nil
	}
	return err
}

// List returns sessions matching a user-metadata query and, if set, a status.
func (c *Client) List(ctx context.Context, query, status string) ([]Session, error) {
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if status != "" {
		q.Set("status", status)
	}
	var out []Session
	err := c.retry(ctx, func() error { return c.do(ctx, http.MethodGet, "/v1/sessions?"+q.Encode(), nil, &out) })
	return out, err
}

// retry runs do until it is not refused with a 429, honouring retry-after,
// for up to c.Wait.
func (c *Client) retry(ctx context.Context, do func() error) error {
	deadline := time.Now().Add(c.Wait)
	for {
		err := do()
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
			return err
		}
		wait := apiErr.RetryAfter
		if wait <= 0 {
			wait = 5 * time.Second
		}
		if time.Now().Add(wait).After(deadline) {
			return &LimitError{Err: apiErr}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-BB-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("browserbase API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		return apiError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("browserbase API: decode %s %s: %w", method, path, err)
	}
	return nil
}

func apiError(resp *http.Response) error {
	var body struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	msg := body.Message
	if msg == "" {
		msg = body.Error
	}
	e := &APIError{Status: resp.StatusCode, Message: msg}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		e.RetryAfter = time.Duration(s) * time.Second
	} else if s, err := strconv.Atoi(resp.Header.Get("X-Ratelimit-Reset")); err == nil {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}
