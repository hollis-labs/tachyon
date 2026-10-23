package actionrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Session is one running pty-backed process. Read yields terminal output;
// Write sends the owner's keystrokes.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	// Wait returns the exit code once the process ends.
	Wait() (int, error)
}

// Transport starts an argv on one machine. It never takes a shell string.
type Transport interface {
	Start(ctx context.Context, argv []string, cols, rows uint16) (Session, error)
	// Output runs argv without a terminal and returns stdout, for read-only
	// lookups such as resource labels.
	Output(ctx context.Context, argv []string) ([]byte, error)
}

// Local executes on the host Tachyon runs on.
type Local struct{}

type localSession struct {
	f   *os.File
	cmd *exec.Cmd
}

func (Local) Start(ctx context.Context, argv []string, cols, rows uint16) (Session, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty argv")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &localSession{f: f, cmd: cmd}, nil
}

func (Local) Output(ctx context.Context, argv []string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
}

func (s *localSession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.f.Write(p) }
func (s *localSession) Resize(cols, rows uint16) error {
	return pty.Setsize(s.f, &pty.Winsize{Cols: cols, Rows: rows})
}
func (s *localSession) Close() error {
	_ = s.f.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}
func (s *localSession) Wait() (int, error) {
	err := s.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// SSH runs argv on a remote host as the configured user. The remote command
// line is built by quoting each argv element; registry parameters are already
// pattern-validated, so the quoting is defence in depth rather than the gate.
type SSH struct {
	Addr           string // host:port
	User           string
	KeyFile        string
	KnownHostsFile string
	// PathPrefix is prepended to the remote PATH (cerberus lives in $HOME/.local/bin).
	PathPrefix string
	// Dial is for tests.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (t *SSH) client(ctx context.Context) (*ssh.Client, error) {
	keyBytes, err := os.ReadFile(t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("ssh key: %w", err)
	}
	hostKey, err := knownhosts.New(t.KnownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("known_hosts: %w", err)
	}
	cfg := &ssh.ClientConfig{User: t.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: hostKey, Timeout: 15 * time.Second}
	dial := t.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 15 * time.Second}).DialContext
	}
	conn, err := dial(ctx, "tcp", t.Addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, t.Addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (t *SSH) remoteLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	line := strings.Join(parts, " ")
	if t.PathPrefix != "" {
		// Operator config, double-quoted so $HOME expands remotely.
		line = `PATH="` + strings.NewReplacer(`"`, "", "`", "", `\`, "").Replace(t.PathPrefix) + `:$PATH" ` + line
	}
	return line
}

type sshSession struct {
	client *ssh.Client
	sess   *ssh.Session
	out    io.Reader
	in     io.WriteCloser
	once   sync.Once
}

func (t *SSH) Start(ctx context.Context, argv []string, cols, rows uint16) (Session, error) {
	client, err := t.client(ctx)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	fail := func(err error) (Session, error) { sess.Close(); client.Close(); return nil, err }
	if err = sess.RequestPty("xterm-256color", int(rows), int(cols), ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}); err != nil {
		return fail(err)
	}
	in, err := sess.StdinPipe()
	if err != nil {
		return fail(err)
	}
	out, err := sess.StdoutPipe() // a pty merges stderr into stdout
	if err != nil {
		return fail(err)
	}
	if err = sess.Start(t.remoteLine(argv)); err != nil {
		return fail(err)
	}
	return &sshSession{client: client, sess: sess, out: out, in: in}, nil
}

func (t *SSH) Output(ctx context.Context, argv []string) ([]byte, error) {
	client, err := t.client(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	return sess.Output(t.remoteLine(argv))
}

func (s *sshSession) Read(p []byte) (int, error)  { return s.out.Read(p) }
func (s *sshSession) Write(p []byte) (int, error) { return s.in.Write(p) }
func (s *sshSession) Resize(cols, rows uint16) error {
	return s.sess.WindowChange(int(rows), int(cols))
}
func (s *sshSession) Close() error {
	s.once.Do(func() { _ = s.in.Close(); _ = s.sess.Close(); _ = s.client.Close() })
	return nil
}
func (s *sshSession) Wait() (int, error) {
	err := s.sess.Wait()
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
