// Package lifecycle exercises the real daemon's process and control-socket
// lifecycle with the real tsgo language server.
package lifecycle

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	pollInterval = 10 * time.Millisecond
	shortWait    = 5 * time.Second
)

var daemonBin string

func TestMain(m *testing.M) {
	root := moduleRoot()
	tmp, err := os.MkdirTemp("", "lifecycle-bin-")
	if err != nil {
		panic(err)
	}
	daemonBin = filepath.Join(tmp, "cgraphd")
	build := exec.Command("go", "build", "-o", daemonBin, "./cmd/cgraphd")
	build.Dir = root
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("lifecycle: building cgraphd: " + err.Error())
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

func moduleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(), "eval", "tiera", "fixtures")
}

func requireLifecycleSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("lifecycle tests require Unix sockets and signals")
	}
	if _, err := exec.LookPath("tsgo"); err != nil {
		t.Skip("tsgo not on PATH")
	}
	probe := filepath.Join(t.TempDir(), "probe.sock")
	ln, err := net.Listen("unix", probe)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	_ = ln.Close()
	_ = os.Remove(probe)
}

type stderrCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *stderrCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

type daemonProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stderr   *stderrCapture
	pidFile  string
	childPID int

	waitDone chan struct{}
	waitMu   sync.Mutex
	exitErr  error
	cleaned  bool
}

func newDaemon(t *testing.T, socket string) *daemonProcess {
	t.Helper()
	realTsgo, err := exec.LookPath("tsgo")
	if err != nil {
		t.Skip("tsgo not on PATH")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "tsgo.pid")
	wrapper := filepath.Join(dir, "tsgo-wrapper.sh")
	script := "#!/bin/sh\necho $$ > \"$CGRAPH_TSGO_PID_FILE\"\nexec \"$CGRAPH_REAL_TSGO\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("writing tsgo wrapper: %v", err)
	}

	stderr := &stderrCapture{}
	cmd := exec.Command(daemonBin,
		"--project-root", fixtureRoot(t),
		"--jsonl", filepath.Join(dir, "telemetry.jsonl"),
		"--session-id", "lifecycle",
		"--graph-mode", "graph",
		"--tsgo", wrapper,
		"--control-socket", socket,
	)
	cmd.Env = append(os.Environ(),
		"CGRAPH_REAL_TSGO="+realTsgo,
		"CGRAPH_TSGO_PID_FILE="+pidFile,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("opening daemon stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting daemon: %v", err)
	}
	d := &daemonProcess{
		cmd:      cmd,
		stdin:    stdin,
		stderr:   stderr,
		pidFile:  pidFile,
		waitDone: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		d.waitMu.Lock()
		d.exitErr = err
		d.waitMu.Unlock()
		close(d.waitDone)
	}()
	t.Cleanup(func() { d.cleanup(t) })
	return d
}

func startDaemon(t *testing.T, socket string) *daemonProcess {
	t.Helper()
	d := newDaemon(t, socket)
	if err := waitForSocket(d, socket, shortWait); err != nil {
		t.Fatalf("waiting for daemon readiness: %v (stderr=%s)", err, d.stderr.String())
	}
	if err := waitForFile(d, d.pidFile, shortWait); err != nil {
		t.Fatalf("waiting for tsgo PID: %v (stderr=%s)", err, d.stderr.String())
	}
	d.childPID = readPID(t, d.pidFile)
	return d
}

func waitForSocket(d *daemonProcess, path string, timeout time.Duration) error {
	return poll(timeout, func() (bool, error) {
		select {
		case <-d.waitDone:
			return false, fmt.Errorf("daemon exited: %v", d.exitError())
		default:
		}
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true, nil
		}
		return false, nil
	})
}

func waitForFile(d *daemonProcess, path string, timeout time.Duration) error {
	return poll(timeout, func() (bool, error) {
		select {
		case <-d.waitDone:
			return false, fmt.Errorf("daemon exited: %v", d.exitError())
		default:
		}
		_, err := os.Stat(path)
		return err == nil, nil
	})
}

func poll(timeout time.Duration, condition func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := condition()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("deadline exceeded")
		}
		remaining := time.Until(deadline)
		wait := pollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		<-timer.C
	}
}

func (d *daemonProcess) exitError() error {
	d.waitMu.Lock()
	defer d.waitMu.Unlock()
	return d.exitErr
}

func (d *daemonProcess) waitForExit(timeout time.Duration) (error, bool) {
	select {
	case <-d.waitDone:
		return d.exitError(), true
	case <-time.After(timeout):
		return nil, false
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading PID file %s: %v", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		t.Fatalf("invalid PID file %s: %q", path, data)
	}
	return pid
}

func pidExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}

func assertPIDGone(t *testing.T, pid int) {
	t.Helper()
	if err := poll(shortWait, func() (bool, error) { return !pidExists(pid), nil }); err != nil {
		t.Errorf("process PID %d did not disappear: %v", pid, err)
	}
}

func assertPathGone(t *testing.T, path string) {
	t.Helper()
	if err := poll(shortWait, func() (bool, error) {
		_, err := os.Lstat(path)
		return os.IsNotExist(err), nil
	}); err != nil {
		t.Errorf("path %s did not disappear: %v", path, err)
	}
}

func (d *daemonProcess) cleanup(t *testing.T) {
	if d.cleaned {
		return
	}
	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	if _, ok := d.waitForExit(shortWait); !ok {
		// Mark the test failed before any fallback process termination.
		t.Errorf("daemon did not exit during cleanup; forcing termination")
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
		if d.childPID != 0 {
			_ = syscall.Kill(d.childPID, syscall.SIGKILL)
		}
		_, _ = d.waitForExit(shortWait)
	}
	if d.childPID != 0 {
		assertPIDGone(t, d.childPID)
	}
	d.cleaned = true
}

func controlCommand(t *testing.T, socket, command string) string {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, shortWait)
	if err != nil {
		t.Fatalf("dialing control socket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(shortWait))
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		t.Fatalf("writing control command: %v", err)
	}
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading control response: %v", err)
	}
	return response
}

func assertIdleConnectionClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(shortWait))
	var one [1]byte
	n, err := conn.Read(one[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("idle control connection was not closed with EOF: n=%d err=%v", n, err)
	}
}

func shutdownViaStdin(t *testing.T, d *daemonProcess) {
	t.Helper()
	if err := d.stdin.Close(); err != nil {
		t.Fatalf("closing daemon stdin: %v", err)
	}
	err, ok := d.waitForExit(shortWait)
	if !ok {
		t.Fatalf("daemon did not exit after stdin disconnect")
	}
	if err != nil {
		t.Fatalf("daemon exited unsuccessfully after stdin disconnect: %v (stderr=%s)", err, d.stderr.String())
	}
}

func TestMCPStdinDisconnectShutsDownEverything(t *testing.T) {
	requireLifecycleSupport(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	d := startDaemon(t, socket)
	if got := controlCommand(t, socket, "sync file.ts"); got != "ok generation=1\n" {
		t.Fatalf("sync response = %q", got)
	}
	if got := controlCommand(t, socket, "unknown command"); got != "err unknown\n" {
		t.Fatalf("unknown response = %q", got)
	}
	idle, err := net.DialTimeout("unix", socket, shortWait)
	if err != nil {
		t.Fatalf("dialing idle control client: %v", err)
	}
	defer idle.Close()

	shutdownViaStdin(t, d)
	assertPIDGone(t, d.childPID)
	assertPathGone(t, socket)
	assertIdleConnectionClosed(t, idle)
}

func TestSIGTERMWithIdleControlClient(t *testing.T) {
	requireLifecycleSupport(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	d := startDaemon(t, socket)
	idle, err := net.DialTimeout("unix", socket, shortWait)
	if err != nil {
		t.Fatalf("dialing idle control client: %v", err)
	}
	defer idle.Close()

	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}
	err, ok := d.waitForExit(shortWait)
	if !ok {
		t.Fatalf("daemon did not exit after SIGTERM")
	}
	if err != nil {
		t.Fatalf("daemon exited unsuccessfully after SIGTERM: %v (stderr=%s)", err, d.stderr.String())
	}
	assertIdleConnectionClosed(t, idle)
	assertPIDGone(t, d.childPID)
	assertPathGone(t, socket)
}

func TestDuplicateLiveSocketStartupCannotStealOwnership(t *testing.T) {
	requireLifecycleSupport(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	first := startDaemon(t, socket)
	originalInfo, err := os.Lstat(socket)
	if err != nil {
		t.Fatalf("stat original socket: %v", err)
	}

	second := newDaemon(t, socket)
	if err := waitForFile(second, second.pidFile, shortWait); err != nil {
		t.Fatalf("waiting for duplicate tsgo PID: %v (stderr=%s)", err, second.stderr.String())
	}
	second.childPID = readPID(t, second.pidFile)
	exitErr, ok := second.waitForExit(shortWait)
	if !ok {
		t.Fatalf("duplicate daemon did not exit promptly")
	}
	if exitErr == nil {
		t.Fatalf("duplicate daemon exited successfully")
	}
	stderr := second.stderr.String()
	if !strings.Contains(stderr, socket) || !strings.Contains(stderr, "already") {
		t.Fatalf("duplicate startup error did not identify socket ownership: %s", stderr)
	}
	assertPIDGone(t, second.childPID)

	info, err := os.Lstat(socket)
	if err != nil || !os.SameFile(originalInfo, info) {
		t.Fatalf("original socket identity changed: before=%v after=%v err=%v", originalInfo, info, err)
	}
	if got := controlCommand(t, socket, "sync file.ts"); got != "ok generation=1\n" {
		t.Fatalf("original sync response = %q", got)
	}
	if got := controlCommand(t, socket, "not-a-command"); got != "err unknown\n" {
		t.Fatalf("original unknown response = %q", got)
	}
	if _, ok := first.waitForExit(50 * time.Millisecond); ok {
		t.Fatalf("original daemon exited while duplicate started: %v", first.exitError())
	}
	shutdownViaStdin(t, first)
	assertPathGone(t, socket)
}

func TestCleanupCannotRemoveReplacementSocket(t *testing.T) {
	requireLifecycleSupport(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	d := startDaemon(t, socket)
	if err := os.Remove(socket); err != nil {
		t.Fatalf("unlinking daemon socket: %v", err)
	}
	replacement, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("binding replacement socket: %v", err)
	}
	defer replacement.Close()
	replacementInfo, err := os.Lstat(socket)
	if err != nil {
		t.Fatalf("stat replacement socket: %v", err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := replacement.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()

	shutdownViaStdin(t, d)
	info, err := os.Lstat(socket)
	if err != nil || !os.SameFile(replacementInfo, info) {
		t.Fatalf("daemon cleanup removed or changed replacement socket: %v", err)
	}
	conn, err := net.DialTimeout("unix", socket, shortWait)
	if err != nil {
		t.Fatalf("dialing replacement socket: %v", err)
	}
	defer conn.Close()
	select {
	case acceptedConn := <-accepted:
		if acceptedConn == nil {
			t.Fatalf("replacement listener did not receive connection")
		}
		_ = acceptedConn.Close()
	case <-time.After(shortWait):
		t.Fatalf("replacement listener did not receive connection")
	}
}

func createStaleSocket(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("creating stale socket: %v", err)
	}
	addr := &syscall.SockaddrUnix{Name: path}
	if err := syscall.Bind(fd, addr); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("binding stale socket: %v", err)
	}
	// A raw Unix socket leaves its pathname behind when its descriptor closes.
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("closing stale socket: %v", err)
	}
}

func TestStaleSocketIsReclaimedSafely(t *testing.T) {
	requireLifecycleSupport(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	createStaleSocket(t, socket)
	if _, err := os.Lstat(socket); err != nil {
		t.Fatalf("stale socket pathname was unexpectedly removed: %v", err)
	}

	d := startDaemon(t, socket)
	if got := controlCommand(t, socket, "sync file.ts"); got != "ok generation=1\n" {
		t.Fatalf("sync response = %q", got)
	}
	shutdownViaStdin(t, d)
	assertPathGone(t, socket)
}

func TestNonSocketPathsAreNeverTreatedAsStale(t *testing.T) {
	requireLifecycleSupport(t)
	cases := []struct {
		name string
		make func(t *testing.T, path string) func(t *testing.T)
	}{
		{
			name: "regular file",
			make: func(t *testing.T, path string) func(t *testing.T) {
				content := []byte("do not remove")
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatalf("creating regular file: %v", err)
				}
				return func(t *testing.T) {
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, content) {
						t.Errorf("regular file changed: %v", err)
					}
				}
			},
		},
		{
			name: "symlink",
			make: func(t *testing.T, path string) func(t *testing.T) {
				target := filepath.Join(filepath.Dir(path), "target")
				if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
					t.Fatalf("creating symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("creating symlink: %v", err)
				}
				return func(t *testing.T) {
					got, err := os.Readlink(path)
					if err != nil || got != target {
						t.Errorf("symlink changed: got=%q err=%v", got, err)
					}
				}
			},
		},
		{
			name: "non-empty directory",
			make: func(t *testing.T, path string) func(t *testing.T) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("creating directory: %v", err)
				}
				child := filepath.Join(path, "keep")
				if err := os.WriteFile(child, []byte("keep"), 0o600); err != nil {
					t.Fatalf("creating directory child: %v", err)
				}
				return func(t *testing.T) {
					if _, err := os.Stat(child); err != nil {
						t.Errorf("directory contents changed: %v", err)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			socket := filepath.Join(dir, "control.sock")
			verify := tc.make(t, socket)
			d := newDaemon(t, socket)
			if err := waitForFile(d, d.pidFile, shortWait); err != nil {
				t.Fatalf("waiting for tsgo PID: %v (stderr=%s)", err, d.stderr.String())
			}
			d.childPID = readPID(t, d.pidFile)
			exitErr, ok := d.waitForExit(shortWait)
			if !ok {
				t.Fatalf("daemon did not reject non-socket path promptly")
			}
			if exitErr == nil {
				t.Fatalf("daemon accepted non-socket path")
			}
			if !strings.Contains(d.stderr.String(), socket) || !strings.Contains(d.stderr.String(), "not a unix socket") {
				t.Fatalf("startup error was not clear: %s", d.stderr.String())
			}
			verify(t)
			assertPIDGone(t, d.childPID)
		})
	}
}
