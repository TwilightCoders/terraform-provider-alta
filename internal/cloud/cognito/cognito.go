// Package cognito authenticates against the AWS Cognito user pool behind the Alta Labs
// portal using USER_SRP_AUTH, without the AWS SDK.
package cognito

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/httpx"
)

// Alta Labs' production user pool, as used by manage.alta.inc.
const (
	AltaRegion     = "us-east-1"
	AltaUserPoolID = "us-east-1_4QbA7N3Uy"
	AltaClientID   = "24bk8l088t5bf31nuceoqb503q"
)

const timestampLayout = "Mon Jan 2 15:04:05 UTC 2006"

// ErrMFARequired is returned when the account needs a second factor, which an
// unattended provider cannot supply.
var ErrMFARequired = errors.New("cognito: account requires MFA; use an account without MFA for automation")

// Tokens is the result of a successful authentication.
type Tokens struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// Config selects a user pool and the collaborators used to reach it.
type Config struct {
	Region     string
	UserPoolID string
	ClientID   string
	// Endpoint overrides https://cognito-idp.<region>.amazonaws.com/ (tests).
	Endpoint   string
	HTTPClient *http.Client
	Now        func() time.Time
	Random     io.Reader
}

// AltaConfig returns the configuration for Alta Labs' production pool.
func AltaConfig() Config {
	return Config{Region: AltaRegion, UserPoolID: AltaUserPoolID, ClientID: AltaClientID}
}

// Client performs Cognito authentication flows.
type Client struct {
	cfg Config
}

// New returns a Client, filling unset collaborators with production defaults.
func New(cfg Config) *Client {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://cognito-idp." + cfg.Region + ".amazonaws.com/"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	return &Client{cfg: cfg}
}

type authResult struct {
	IDToken      string `json:"IdToken"`
	AccessToken  string `json:"AccessToken"`
	RefreshToken string `json:"RefreshToken"`
	ExpiresIn    int    `json:"ExpiresIn"`
}

type challengeResponse struct {
	ChallengeName        string            `json:"ChallengeName"`
	ChallengeParameters  map[string]string `json:"ChallengeParameters"`
	AuthenticationResult *authResult       `json:"AuthenticationResult"`
}

// Login authenticates username/password with SRP.
func (c *Client) Login(ctx context.Context, username, password string) (Tokens, error) {
	if username == "" || password == "" {
		return Tokens{}, errors.New("cognito: username and password are required")
	}
	session, err := newSRPSession(c.poolName(), c.cfg.Random)
	if err != nil {
		return Tokens{}, err
	}

	var challenge challengeResponse
	if err := c.call(ctx, "InitiateAuth", map[string]any{
		"AuthFlow":       "USER_SRP_AUTH",
		"ClientId":       c.cfg.ClientID,
		"AuthParameters": map[string]string{"USERNAME": username, "SRP_A": session.PublicHex()},
	}, &challenge); err != nil {
		return Tokens{}, err
	}
	if err := requireChallenge(challenge, "PASSWORD_VERIFIER"); err != nil {
		return Tokens{}, err
	}

	params := challenge.ChallengeParameters
	timestamp := c.cfg.Now().UTC().Format(timestampLayout)
	signature, err := session.Signature(params["USER_ID_FOR_SRP"], password, params["SRP_B"], params["SALT"], params["SECRET_BLOCK"], timestamp)
	if err != nil {
		return Tokens{}, err
	}

	var result challengeResponse
	if err := c.call(ctx, "RespondToAuthChallenge", map[string]any{
		"ChallengeName": "PASSWORD_VERIFIER",
		"ClientId":      c.cfg.ClientID,
		"ChallengeResponses": map[string]string{
			"USERNAME":                    params["USER_ID_FOR_SRP"],
			"PASSWORD_CLAIM_SECRET_BLOCK": params["SECRET_BLOCK"],
			"PASSWORD_CLAIM_SIGNATURE":    signature,
			"TIMESTAMP":                   timestamp,
		},
	}, &result); err != nil {
		return Tokens{}, err
	}
	return c.tokens(result, "")
}

// Refresh exchanges a refresh token for new id and access tokens.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	var result challengeResponse
	if err := c.call(ctx, "InitiateAuth", map[string]any{
		"AuthFlow":       "REFRESH_TOKEN_AUTH",
		"ClientId":       c.cfg.ClientID,
		"AuthParameters": map[string]string{"REFRESH_TOKEN": refreshToken},
	}, &result); err != nil {
		return Tokens{}, err
	}
	return c.tokens(result, refreshToken)
}

func (c *Client) poolName() string {
	_, name, _ := strings.Cut(c.cfg.UserPoolID, "_")
	return name
}

func (c *Client) tokens(r challengeResponse, fallbackRefresh string) (Tokens, error) {
	if r.ChallengeName != "" {
		return Tokens{}, requireChallenge(r, "")
	}
	if r.AuthenticationResult == nil || r.AuthenticationResult.IDToken == "" {
		return Tokens{}, errors.New("cognito: response carried no tokens")
	}
	a := r.AuthenticationResult
	refresh := a.RefreshToken
	if refresh == "" {
		refresh = fallbackRefresh
	}
	return Tokens{
		IDToken:      a.IDToken,
		AccessToken:  a.AccessToken,
		RefreshToken: refresh,
		ExpiresAt:    c.cfg.Now().Add(time.Duration(a.ExpiresIn) * time.Second),
	}, nil
}

func requireChallenge(r challengeResponse, want string) error {
	switch r.ChallengeName {
	case want:
		return nil
	case "SOFTWARE_TOKEN_MFA", "SMS_MFA":
		return ErrMFARequired
	default:
		return fmt.Errorf("cognito: unexpected challenge %q", r.ChallengeName)
	}
}

func (c *Client) call(ctx context.Context, action string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+action)

	status, data, err := httpx.Do(c.cfg.HTTPClient, req)
	if err != nil {
		return fmt.Errorf("cognito %s: %w", action, err)
	}
	if status != http.StatusOK {
		var apiErr struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		kind := apiErr.Type[strings.LastIndex(apiErr.Type, "#")+1:]
		return fmt.Errorf("cognito %s: %s: %s", action, kind, apiErr.Message)
	}
	return json.Unmarshal(data, out)
}
