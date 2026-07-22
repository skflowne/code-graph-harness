package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/skflowne/code-graph-harness/internal/core"
)

// SocketPath derives the project-keyed control-socket path for cfg:
// cfg.ControlSocket verbatim if set, otherwise
// /tmp/cgraphd-<12-hex-char-hash-of-ProjectRoot>.sock, so one daemon per
// project root gets a stable, collision-resistant path without requiring
// config.
func SocketPath(cfg core.Config) string {
	if cfg.ControlSocket != "" {
		return cfg.ControlSocket
	}
	sum := sha256.Sum256([]byte(cfg.ProjectRoot))
	return fmt.Sprintf("/tmp/cgraphd-%s.sock", hex.EncodeToString(sum[:])[:12])
}

// ControlSocket is the Phase 0 scaffold for the Phase 1 staleness barrier: a
// unix-socket listener that accepts newline-delimited text commands and
// replies newline-delimited text. It understands exactly one command family
// today:
//
//	sync <file>   -> bumps the shared GenerationCounter, replies "ok generation=<n>\n"
//	<anything else> -> replies "err unknown\n"
//
// Phase 1 will replace/extend "sync" with real edit-observation logic and
// per-file staleness bookkeeping; the wire protocol and goroutine lifecycle
// established here are meant to carry forward unchanged.
type ControlSocket struct {
	path string
	gen  *core.GenerationCounter

	listener net.Listener
	lockFile *os.File
	// socketInfo identifies the inode created by net.Listen. It prevents a
	// later owner from being removed when this instance shuts down.
	socketInfo os.FileInfo

	acceptWG     sync.WaitGroup
	handlers     sync.WaitGroup
	connMu       sync.Mutex
	connections  map[net.Conn]struct{}
	shuttingDown bool
	shutdownOnce sync.Once
	cleanupOnce  sync.Once
}

// NewControlSocket builds a ControlSocket bound to path, bumping/reading gen
// on commands. Call Start to begin listening.
func NewControlSocket(path string, gen *core.GenerationCounter) *ControlSocket {
	return &ControlSocket{path: path, gen: gen, connections: make(map[net.Conn]struct{})}
}

// Path returns the unix-socket path this ControlSocket listens (or will
// listen) on.
func (c *ControlSocket) Path() string { return c.path }

// Start acquires exclusive ownership of c.Path(), binds a new unix-socket
// listener, and begins accepting connections in a background goroutine. A
// companion lock file is intentionally persistent; flock releases ownership
// when the process exits without introducing an unlink race.
func (c *ControlSocket) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("control socket: nil context")
	}

	lockPath := c.path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("control socket: opening ownership lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("control socket %s is already owned by another daemon", c.path)
		}
		return fmt.Errorf("control socket: locking %s: %w", lockPath, err)
	}
	c.lockFile = lockFile

	if err := c.preparePath(); err != nil {
		c.releaseLock()
		return err
	}

	ln, err := net.Listen("unix", c.path)
	if err != nil {
		c.releaseLock()
		return fmt.Errorf("control socket: listen %s: %w", c.path, err)
	}
	// net.UnixListener.Close normally unlinks its pathname. Disable that
	// behavior so cleanup can remove the path only while it still names our
	// listener; otherwise closing an unlinked listener could remove a
	// replacement socket that another owner installed at the same path.
	if unixListener, ok := ln.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	info, err := os.Lstat(c.path)
	if err != nil {
		_ = ln.Close()
		c.releaseLock()
		return fmt.Errorf("control socket: stat bound socket %s: %w", c.path, err)
	}
	c.listener = ln
	c.socketInfo = info

	c.acceptWG.Add(1)
	go c.acceptLoop()
	go func() {
		<-ctx.Done()
		c.beginShutdown()
	}()
	return nil
}

// preparePath rejects unsafe existing paths, probes socket paths for a live
// listener, and removes only sockets confirmed to be stale.
func (c *ControlSocket) preparePath() error {
	info, err := os.Lstat(c.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("control socket: inspecting %s: %w", c.path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control socket %s already exists and is not a unix socket", c.path)
	}

	conn, err := net.DialTimeout("unix", c.path, 250*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("control socket %s already has a live listener", c.path)
	}
	if !confirmedStaleSocket(err) {
		return fmt.Errorf("control socket: probing %s: %w", c.path, err)
	}
	if err := os.Remove(c.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("control socket: removing stale socket %s: %w", c.path, err)
	}
	return nil
}

func confirmedStaleSocket(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ENOTCONN)
}

// Wait blocks until the accept loop and all in-flight connections have
// finished. Call it after cancelling the context passed to Start.
func (c *ControlSocket) Wait() {
	c.acceptWG.Wait()
	c.handlers.Wait()
	c.cleanup()
}

func (c *ControlSocket) beginShutdown() {
	c.shutdownOnce.Do(func() {
		// Close the listener first. This unblocks Accept before the state lock
		// is changed, so an accepted connection can be classified atomically.
		if c.listener != nil {
			_ = c.listener.Close()
		}
		c.connMu.Lock()
		c.shuttingDown = true
		for conn := range c.connections {
			_ = conn.Close()
		}
		c.connMu.Unlock()
	})
}

func (c *ControlSocket) cleanup() {
	c.cleanupOnce.Do(func() {
		if c.socketInfo != nil {
			if info, err := os.Lstat(c.path); err == nil && os.SameFile(info, c.socketInfo) {
				_ = os.Remove(c.path)
			}
		}
		c.releaseLock()
	})
}

func (c *ControlSocket) releaseLock() {
	if c.lockFile == nil {
		return
	}
	_ = syscall.Flock(int(c.lockFile.Fd()), syscall.LOCK_UN)
	_ = c.lockFile.Close()
	c.lockFile = nil
}

func (c *ControlSocket) acceptLoop() {
	defer c.acceptWG.Done()
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return
		}

		c.connMu.Lock()
		if c.shuttingDown {
			c.connMu.Unlock()
			_ = conn.Close()
			return
		}
		c.connections[conn] = struct{}{}
		c.handlers.Add(1)
		c.connMu.Unlock()

		go func() {
			defer c.handlers.Done()
			defer func() {
				c.connMu.Lock()
				delete(c.connections, conn)
				c.connMu.Unlock()
			}()
			c.handleConn(conn)
		}()
	}
}

func (c *ControlSocket) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if _, err := conn.Write([]byte(c.handleCommand(line))); err != nil {
			return
		}
	}
}

func (c *ControlSocket) handleCommand(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "err unknown\n"
	}
	switch fields[0] {
	case "sync":
		n := c.gen.Bump()
		return fmt.Sprintf("ok generation=%d\n", n)
	default:
		return "err unknown\n"
	}
}
