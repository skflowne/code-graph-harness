package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

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
	path     string
	gen      *core.GenerationCounter
	listener net.Listener
	wg       sync.WaitGroup
}

// NewControlSocket builds a ControlSocket bound to path, bumping/reading gen
// on commands. Call Start to begin listening.
func NewControlSocket(path string, gen *core.GenerationCounter) *ControlSocket {
	return &ControlSocket{path: path, gen: gen}
}

// Path returns the unix-socket path this ControlSocket listens (or will
// listen) on.
func (c *ControlSocket) Path() string { return c.path }

// Start removes any stale socket file at c.Path(), binds a new unix-socket
// listener, and begins accepting connections in a background goroutine.
// Start returns once the listener is ready (or setup failed); shutdown of
// the accept loop and connections happens asynchronously when ctx is
// cancelled. Call Wait after cancelling ctx to block until fully stopped.
func (c *ControlSocket) Start(ctx context.Context) error {
	if err := os.RemoveAll(c.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("control socket: removing stale socket %s: %w", c.path, err)
	}

	ln, err := net.Listen("unix", c.path)
	if err != nil {
		return fmt.Errorf("control socket: listen %s: %w", c.path, err)
	}
	c.listener = ln

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.RemoveAll(c.path)
	}()

	c.wg.Add(1)
	go c.acceptLoop(ctx)
	return nil
}

// Wait blocks until the accept loop and all in-flight connections have
// finished. Call it after cancelling the context passed to Start.
func (c *ControlSocket) Wait() {
	c.wg.Wait()
}

func (c *ControlSocket) acceptLoop(ctx context.Context) {
	defer c.wg.Done()
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			// Expected once ctx is cancelled and the listener is closed by
			// the goroutine started in Start; anything else also just stops
			// the loop (a dead listener has nothing left to accept).
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
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
