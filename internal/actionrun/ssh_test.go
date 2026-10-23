package actionrun

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshServer accepts one key, grants a pty, "runs" a command by echoing what it
// was asked to run plus anything typed, and exits 7.
func sshServer(t *testing.T, clientKey ssh.PublicKey) (addr string, hostKey ssh.PublicKey, windows chan [2]uint32) {
	t.Helper()
	_, hpriv, _ := ed25519.GenerateKey(rand.Reader)
	hsigner, _ := ssh.NewSignerFromKey(hpriv)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(clientKey.Marshal()) {
			return nil, nil
		}
		return nil, os.ErrPermission
	}}
	cfg.AddHostKey(hsigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	windows = make(chan [2]uint32, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, creqs, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						for r := range creqs {
							switch r.Type {
							case "pty-req":
								r.Reply(true, nil)
							case "window-change":
								r.Reply(false, nil)
							case "exec":
								r.Reply(true, nil)
								cmd := string(r.Payload[4:])
								ch.Write([]byte("ran:" + cmd + "\r\n"))
								buf := make([]byte, 64)
								n, _ := ch.Read(buf)
								ch.Write([]byte("typed:" + string(buf[:n])))
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{7}))
								ch.Close()
							default:
								r.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	return ln.Addr().String(), hsigner.PublicKey(), windows
}

func TestSSHTransportPtyAndExitStatus(t *testing.T) {
	dir := t.TempDir()
	cpub, cpriv, _ := ed25519.GenerateKey(rand.Reader)
	blk, err := ssh.MarshalPrivateKey(cpriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "id")
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(blk), 0o600)
	spub, _ := ssh.NewPublicKey(cpub)
	addr, hostKey, _ := sshServer(t, spub)
	known := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(known, []byte("["+strings.Replace(addr, ":", "]:", 1)+" "+string(ssh.MarshalAuthorizedKey(hostKey))), 0o600)

	tr := &SSH{Addr: addr, User: "chrispian", KeyFile: keyFile, KnownHostsFile: known, PathPrefix: "$HOME/.local/bin"}
	s, err := tr.Start(context.Background(), []string{"cerberus", "resource", "reload", "hub-tether", "--ack"}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := s.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	code, err := s.Wait()
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), `ran:PATH="$HOME/.local/bin:$PATH" cerberus`) || !strings.Contains(out.String(), "cerberus resource reload hub-tether --ack") || !strings.Contains(out.String(), "typed:hi") {
		t.Fatalf("out=%q", out.String())
	}
	_ = s.Close()

	bad := &SSH{Addr: addr, User: "chrispian", KeyFile: keyFile, KnownHostsFile: filepath.Join(dir, "other_known_hosts")}
	_ = os.WriteFile(bad.KnownHostsFile, nil, 0o600)
	if _, err := bad.Start(context.Background(), []string{"x"}, 80, 24); err == nil {
		t.Fatal("unknown host key must be refused")
	}
}
