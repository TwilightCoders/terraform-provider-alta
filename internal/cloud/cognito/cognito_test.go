package cognito

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 9, 14, 22, 30, 5, 0, time.UTC)

// fakePool scripts Cognito responses keyed by X-Amz-Target action.
type fakePool struct {
	t         *testing.T
	responses map[string][]string
	requests  map[string][]map[string]any
}

func newFakePool(t *testing.T, responses map[string][]string) (*fakePool, *Client) {
	t.Helper()
	p := &fakePool{t: t, responses: responses, requests: map[string][]map[string]any{}}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, New(Config{
		UserPoolID: vectorPoolID(),
		ClientID:   "client-1",
		Endpoint:   srv.URL,
		Now:        func() time.Time { return fixedNow },
		Random:     vectorSecretReader(),
	})
}

func vectorPoolID() string { return "us-east-1_" + vectorPool }

func (p *fakePool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "AWSCognitoIdentityProviderService.")
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		p.t.Errorf("decoding %s body: %v", action, err)
	}
	p.requests[action] = append(p.requests[action], body)

	queue := p.responses[action]
	if len(queue) == 0 {
		p.t.Errorf("unexpected %s call", action)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	p.responses[action] = queue[1:]
	if strings.HasPrefix(queue[0], "!") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(queue[0][1:]))
		return
	}
	_, _ = w.Write([]byte(queue[0]))
}

func passwordVerifier() string {
	b, _ := json.Marshal(map[string]any{
		"ChallengeName": "PASSWORD_VERIFIER",
		"ChallengeParameters": map[string]string{
			"USER_ID_FOR_SRP": vectorUserID,
			"SRP_B":           vectorB,
			"SALT":            vectorSalt,
			"SECRET_BLOCK":    vectorSecret,
		},
	})
	return string(b)
}

const tokensJSON = `{"AuthenticationResult":{"IdToken":"id","AccessToken":"access","RefreshToken":"refresh","ExpiresIn":3600}}`

func TestLoginCompletesSRPFlow(t *testing.T) {
	pool, c := newFakePool(t, map[string][]string{
		"InitiateAuth":           {passwordVerifier()},
		"RespondToAuthChallenge": {tokensJSON},
	})

	tok, err := c.Login(context.Background(), "user@example.com", vectorPassword)
	if err != nil {
		t.Fatal(err)
	}
	want := Tokens{IDToken: "id", AccessToken: "access", RefreshToken: "refresh", ExpiresAt: fixedNow.Add(time.Hour)}
	if tok != want {
		t.Fatalf("tokens = %+v, want %+v", tok, want)
	}

	initiate := pool.requests["InitiateAuth"][0]["AuthParameters"].(map[string]any)
	if !strings.EqualFold(initiate["SRP_A"].(string), vectorA) || initiate["USERNAME"] != "user@example.com" {
		t.Errorf("InitiateAuth parameters = %v", initiate)
	}
	responses := pool.requests["RespondToAuthChallenge"][0]["ChallengeResponses"].(map[string]any)
	if responses["PASSWORD_CLAIM_SIGNATURE"] != vectorSignature || responses["TIMESTAMP"] != vectorTimestamp {
		t.Errorf("challenge responses = %v", responses)
	}
}

func TestLoginReportsMFA(t *testing.T) {
	_, c := newFakePool(t, map[string][]string{
		"InitiateAuth":           {passwordVerifier()},
		"RespondToAuthChallenge": {`{"ChallengeName":"SOFTWARE_TOKEN_MFA","Session":"s"}`},
	})
	if _, err := c.Login(context.Background(), "u", vectorPassword); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("err = %v, want ErrMFARequired", err)
	}
}

func TestLoginSurfacesCognitoErrors(t *testing.T) {
	_, c := newFakePool(t, map[string][]string{
		"InitiateAuth": {`!{"__type":"com.amazonaws#NotAuthorizedException","message":"Incorrect username or password."}`},
	})
	_, err := c.Login(context.Background(), "u", "wrong")
	if err == nil || !strings.Contains(err.Error(), "NotAuthorizedException: Incorrect username or password.") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	c := New(AltaConfig())
	if _, err := c.Login(context.Background(), "", "x"); err == nil {
		t.Fatal("expected error for empty username")
	}
}

func TestRefreshKeepsRefreshToken(t *testing.T) {
	_, c := newFakePool(t, map[string][]string{
		"InitiateAuth": {`{"AuthenticationResult":{"IdToken":"id2","AccessToken":"a2","ExpiresIn":60}}`},
	})
	tok, err := c.Refresh(context.Background(), "keep-me")
	if err != nil {
		t.Fatal(err)
	}
	if tok.IDToken != "id2" || tok.RefreshToken != "keep-me" {
		t.Fatalf("tokens = %+v", tok)
	}
}
