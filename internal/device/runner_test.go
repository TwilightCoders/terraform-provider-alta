package device

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshServer answers exec requests by echoing the command and stdin; a command of
// "fail" exits 1 with a message on stderr.
type sshServer struct {
	addr    string
	hostKey ssh.PublicKey
}

func newSSHServer(t *testing.T, clientKey ssh.PublicKey) *sshServer {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	must(t, err)

	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) == string(clientKey.Marshal()) {
			return &ssh.Permissions{}, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSSH(conn, cfg)
		}
	}()
	return &sshServer{addr: ln.Addr().String(), hostKey: hostSigner.PublicKey()}
}

func serveSSH(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		ch, requests, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer func() { _ = ch.Close() }()
			for req := range requests {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				cmd := string(req.Payload[4:])
				status := uint32(0)
				if cmd == "fail" {
					_, _ = ch.Stderr().Write([]byte("it broke"))
					status = 1
				} else {
					input, _ := io.ReadAll(ch)
					_, _ = ch.Write([]byte(cmd + "|" + string(input)))
				}
				_, _ = ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
				return
			}
		}()
	}
}

func clientKeyPEM(t *testing.T) ([]byte, ssh.PublicKey) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	must(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	must(t, err)
	return pem.EncodeToMemory(block), signer.PublicKey()
}

func runnerFor(t *testing.T, srv *sshServer, key []byte, fingerprint string) *SSHRunner {
	t.Helper()
	host, port, _ := net.SplitHostPort(srv.addr)
	p, _ := strconv.Atoi(port)
	r, err := NewSSHRunner(SSHConfig{Host: host, Port: p, PrivateKey: key, HostKeyFingerprint: fingerprint})
	must(t, err)
	return r
}

func TestSSHRunnerRunsScriptsWithStdin(t *testing.T) {
	key, pub := clientKeyPEM(t)
	srv := newSSHServer(t, pub)
	r := runnerFor(t, srv, key, Fingerprint(srv.hostKey))

	out, err := r.Run(context.Background(), "status", []byte("journal"))
	must(t, err)
	if string(out) != "status|journal" {
		t.Fatalf("out = %q", out)
	}

	_, err = r.Run(context.Background(), "fail", nil)
	var se *ScriptError
	if !errors.As(err, &se) || !strings.Contains(se.Stderr, "it broke") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSHRunnerRejectsUnpinnedHostKey(t *testing.T) {
	key, pub := clientKeyPEM(t)
	srv := newSSHServer(t, pub)
	r := runnerFor(t, srv, key, "SHA256:not-the-router")

	if _, err := r.Run(context.Background(), "status", nil); err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewSSHRunnerValidates(t *testing.T) {
	for name, cfg := range map[string]SSHConfig{
		"host":        {HostKeyFingerprint: "SHA256:x", PrivateKey: []byte("k")},
		"fingerprint": {Host: "h", PrivateKey: []byte("k")},
		"credentials": {Host: "h", HostKeyFingerprint: "SHA256:x"},
	} {
		if _, err := NewSSHRunner(cfg); err == nil {
			t.Errorf("missing %s: expected error", name)
		}
	}
}
