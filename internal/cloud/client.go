// Package cloud is a client for the Alta Labs management API behind manage.alta.inc.
//
// The API is unpublished. Objects are handled as raw JSON documents (Object) so fields
// this provider does not model survive a read-modify-write untouched.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cognito"
	"github.com/TwilightCoders/terraform-provider-alta/internal/httpx"
)

// DefaultBaseURL is Alta Labs' production management API.
const DefaultBaseURL = "https://manage.alta.inc"

// tokenSkew refreshes tokens this long before Cognito would reject them.
const tokenSkew = time.Minute

// Object is a JSON object as returned by the API, numbers preserved as json.Number.
type Object = map[string]any

// Authenticator obtains Cognito tokens.
type Authenticator interface {
	Login(ctx context.Context, username, password string) (cognito.Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (cognito.Tokens, error)
}

// Config configures a Client.
type Config struct {
	BaseURL    string
	Email      string
	Password   string
	Auth       Authenticator
	HTTPClient *http.Client
	Now        func() time.Time
}

// Client talks to the Alta Labs API. It is safe for concurrent use.
type Client struct {
	cfg    Config
	mu     sync.Mutex
	tokens cognito.Tokens
}

// NewClient returns a Client, filling unset collaborators with production defaults.
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Auth == nil {
		cfg.Auth = cognito.New(cognito.AltaConfig())
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{cfg: cfg}
}

// StatusError is a non-2xx API response.
type StatusError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("alta api %s %s: HTTP %d: %.200s", e.Method, e.Path, e.Status, e.Body)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body Object) error {
	return c.do(ctx, http.MethodPost, path, nil, body, nil)
}

// do performs one request, re-authenticating once if the token is rejected.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body Object, out any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := c.request(ctx, method, path, query, body, token)
		if err != nil {
			return err
		}
		status, data, err := httpx.Do(c.cfg.HTTPClient, req)
		if err != nil {
			return fmt.Errorf("alta api %s %s: %w", method, path, err)
		}
		if (status == http.StatusUnauthorized || status == http.StatusForbidden) && attempt == 0 {
			continue
		}
		if status < 200 || status > 299 {
			return &StatusError{Method: method, Path: path, Status: status, Body: string(data)}
		}
		if out == nil {
			return nil
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("alta api %s %s: decoding response: %w", method, path, err)
		}
		return nil
	}
}

// request builds a request. The portal sends the Cognito id token as a header on reads
// and inside the JSON body on writes; some endpoints accept only that form.
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body Object, token string) (*http.Request, error) {
	target := c.cfg.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	if body == nil {
		req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("token", token)
		return req, nil
	}

	withToken := make(Object, len(body)+1)
	for k, v := range body {
		withToken[k] = v
	}
	withToken["token"] = token
	payload, err := json.Marshal(withToken)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) token(ctx context.Context, forceLogin bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fresh := c.tokens.IDToken != "" && c.cfg.Now().Add(tokenSkew).Before(c.tokens.ExpiresAt)
	switch {
	case fresh && !forceLogin:
		return c.tokens.IDToken, nil
	case c.tokens.RefreshToken != "" && !forceLogin:
		if t, err := c.cfg.Auth.Refresh(ctx, c.tokens.RefreshToken); err == nil {
			c.tokens = t
			return t.IDToken, nil
		}
	}

	t, err := c.cfg.Auth.Login(ctx, c.cfg.Email, c.cfg.Password)
	if err != nil {
		return "", fmt.Errorf("alta api: authenticating: %w", err)
	}
	c.tokens = t
	return t.IDToken, nil
}
