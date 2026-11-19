package chatterminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Session is one pty-backed ssh process.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	// Wait returns the exit code once the process ends.
	Wait() (int, error)
	// Interrupt asks the process group to end (SIGTERM); Close kills it.
	Interrupt()
}

// Starter launches argv in a pty with the given environment. Tests substitute it.
type Starter func(ctx context.Context, argv, env []string, cols, rows uint16) (Session, error)

// sshArgv builds the one fixed template. Only the machine's resolved host alias and user are inputs, and
// the host cannot start with "-" (hostPattern). exec adds a forced pty so closing the channel hangs up the
// remote command; its output is the merged pty stream.
func sshArgv(bin string, m Machine, command string, exec bool) []string {
	argv := []string{bin,
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "PermitLocalCommand=no",
		"-o", "ServerAliveInterval=15",
	}
	if exec {
		argv = append(argv, "-o", "BatchMode=yes", "-tt")
	} else {
		argv = append(argv, "-o", "BatchMode=no", "-t")
	}
	if m.SSHUser != "" {
		argv = append(argv, "-l", m.SSHUser)
	}
	argv = append(argv, m.host)
	if exec {
		argv = append(argv, "--", command)
	}
	return argv
}

// sshEnv is the minimum environment for ssh: the user's agent and config, nothing else from Tachyon's own.
func sshEnv(sock string) []string {
	env := []string{"TERM=xterm-256color", "LANG=C.UTF-8"}
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if sock != "" {
		env = append(env, "SSH_AUTH_SOCK="+sock)
	}
	return env
}

type localSession struct {
	f    *os.File
	cmd  *exec.Cmd
	stop chan struct{}
	once sync.Once
}

func (s *localSession) signal(sig syscall.Signal) {
	if s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, sig)
	}
}

// StartPTY is the real Starter. The child leads its own session, so its pid is the process group id.
func StartPTY(ctx context.Context, argv, env []string, cols, rows uint16) (Session, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty argv")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	s := &localSession{f: f, cmd: cmd, stop: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			s.signal(syscall.SIGKILL)
		case <-s.stop:
		}
	}()
	return s, nil
}

func (s *localSession) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *localSession) Write(p []byte) (int, error) { return s.f.Write(p) }
func (s *localSession) Resize(cols, rows uint16) error {
	return pty.Setsize(s.f, &pty.Winsize{Cols: cols, Rows: rows})
}
func (s *localSession) Interrupt() { s.signal(syscall.SIGTERM) }
func (s *localSession) Close() error {
	s.once.Do(func() {
		close(s.stop)
		_ = s.f.Close()
		s.signal(syscall.SIGKILL)
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
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

// stopGrace is how long Stop waits after SIGTERM before SIGKILL.
const stopGrace = 5 * time.Second

// SessionsPrefixPath is where the sessions routes live; ws_path in a session row points under it.
const SessionsPrefixPath = "/api/plugins/console/v1/terminal/sessions"
