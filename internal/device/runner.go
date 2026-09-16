// Package device operates the commit-confirmed gate on an Alta router over SSH:
// pausing its cloud agent, journaling a transaction, arming and disarming a
// router-enforced rollback, and probing health.
package device

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Runner executes a shell script on the router with the given stdin.
type Runner interface {
	Run(ctx context.Context, script string, stdin []byte) ([]byte, error)
}

// SSHConfig describes how to reach the router.
type SSHConfig struct {
	Host string
	Port int
	User string
	// PrivateKey is a PEM private key. When empty, the SSH agent at AgentSocket is used.
	PrivateKey []byte
	// AgentSocket is typically $SSH_AUTH_SOCK.
	AgentSocket string
	// HostKeyFingerprint pins the router's host key, in OpenSSH form ("SHA256:…").
	HostKeyFingerprint string
	DialTimeout        time.Duration
}

// SSHRunner dials a fresh connection per script. The router restarts networking on
// every config push, so long-lived connections are exactly what not to trust.
type SSHRunner struct {
	cfg  SSHConfig
	gate *dialGate
}

// NewSSHRunner validates cfg and returns a runner.
func NewSSHRunner(cfg SSHConfig) (*SSHRunner, error) {
	if cfg.Host == "" {
		return nil, errors.New("ssh: host is required")
	}
	if cfg.HostKeyFingerprint == "" {
		return nil, errors.New("ssh: host_key_fingerprint is required; the router's host key must be pinned")
	}
	if len(cfg.PrivateKey) == 0 && cfg.AgentSocket == "" {
		return nil, errors.New("ssh: provide a private key or an SSH agent")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	return &SSHRunner{cfg: cfg, gate: newDialGate(1)}, nil
}

// Run executes script via the remote shell and returns stdout.
func (r *SSHRunner) Run(ctx context.Context, script string, stdin []byte) ([]byte, error) {
	return r.gate.do(ctx, func() ([]byte, error) { return r.run(ctx, script, stdin) })
}

func (r *SSHRunner) run(ctx context.Context, script string, stdin []byte) ([]byte, error) {
	client, err := r.dial(ctx)
	if err != nil {
		return nil, &DialError{Err: err}
	}
	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		// The connection came up and then went away before the script could start.
		return nil, &DialError{Err: fmt.Errorf("ssh: opening session: %w", err)}
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout, session.Stderr = &stdout, &stderr
	session.Stdin = bytes.NewReader(stdin)

	done := make(chan error, 1)
	go func() { done <- session.Run(script) }()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return stdout.Bytes(), ctx.Err()
	case err := <-done:
		if err != nil {
			return stdout.Bytes(), &ScriptError{Err: err, Stderr: stderr.String()}
		}
		return stdout.Bytes(), nil
	}
}

// DialError is a connection that never carried a script. The router's SSH server resets
// connections that arrive together, so these are worth retrying; a script that ran is not.
type DialError struct{ Err error }

func (e *DialError) Error() string { return e.Err.Error() }

func (e *DialError) Unwrap() error { return e.Err }

// ScriptError is a script that ran but failed.
type ScriptError struct {
	Err    error
	Stderr string
}

func (e *ScriptError) Error() string {
	return fmt.Sprintf("router script failed: %v: %.500s", e.Err, e.Stderr)
}

func (e *ScriptError) Unwrap() error { return e.Err }

func (r *SSHRunner) dial(ctx context.Context) (*ssh.Client, error) {
	auth, closeAuth, err := r.auth()
	if err != nil {
		return nil, err
	}
	defer closeAuth()

	addr := net.JoinHostPort(r.cfg.Host, strconv.Itoa(r.cfg.Port))
	conn, err := (&net.Dialer{Timeout: r.cfg.DialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh: dialing %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            r.cfg.User,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: pinnedHostKey(r.cfg.HostKeyFingerprint),
		Timeout:         r.cfg.DialTimeout,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh: handshake with %s: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (r *SSHRunner) auth() (ssh.AuthMethod, func(), error) {
	if len(r.cfg.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(r.cfg.PrivateKey)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh: parsing private key: %w", err)
		}
		return ssh.PublicKeys(signer), func() {}, nil
	}
	conn, err := net.Dial("unix", r.cfg.AgentSocket)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh: connecting to agent: %w", err)
	}
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers), func() { _ = conn.Close() }, nil
}

// Fingerprint renders a host key the way `ssh-keygen -lf` does.
func Fingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func pinnedHostKey(want string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if got := Fingerprint(key); got != want {
			return fmt.Errorf("host key mismatch: router presented %s, expected %s", got, want)
		}
		return nil
	}
}
