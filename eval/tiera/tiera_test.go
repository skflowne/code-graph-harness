// Package tiera is the Phase 0 Tier A gate: a retrieval-correctness harness
// that drives the real cgraphd daemon over MCP (stdio, via CommandTransport)
// against a pinned TypeScript fixture and asserts the three passthrough tools
// return the expected definitions, references, and outline.
//
// It doubles as the Phase 0 end-to-end check: it exercises the actual daemon
// binary + real tsgo LSP provider + real JSONL telemetry — not stubs — and
// verifies "every call is logged". Run it with:
//
//	go test ./eval/tiera/ -v
//
// tsgo must be on PATH (it is what the daemon spawns).
package tiera

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/skflowne/code-graph-harness/internal/core"
	cgmcp "github.com/skflowne/code-graph-harness/internal/mcp"
	"github.com/skflowne/code-graph-harness/internal/tools"
)

var daemonBin string // built once in TestMain

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tiera-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	daemonBin = filepath.Join(tmp, "cgraphd")
	// Build from the module root (two levels up from eval/tiera).
	build := exec.Command("go", "build", "-o", daemonBin, "./cmd/cgraphd")
	build.Dir = filepath.Join("..", "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("tiera: building cgraphd: " + err.Error())
	}
	os.Exit(m.Run())
}

// fixtureRoot is the absolute path to the pinned fixture project.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("fixtures")
	if err != nil {
		t.Fatalf("resolving fixture root: %v", err)
	}
	return root
}

type daemonProcess struct {
	cmd      *exec.Cmd
	sess     *mcp.ClientSession
	stderr   *bytes.Buffer
	tsgoPID  string
	waitDone chan error
	waited   bool
	closed   bool
}

func makeDaemonCommand(t *testing.T, root, jsonl, pidFile string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	realTsgo, err := exec.LookPath("tsgo")
	if err != nil {
		t.Skip("tsgo not on PATH; skipping daemon lifecycle coverage")
	}
	wrapper := filepath.Join(t.TempDir(), "tsgo-wrapper.sh")
	script := "#!/bin/sh\necho $$ > \"$CGRAPH_TSGO_PID_FILE\"\nexec \"$CGRAPH_REAL_TSGO\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("writing tsgo wrapper: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd := exec.Command(daemonBin,
		"--project-root", root,
		"--jsonl", jsonl,
		"--graph-mode", "graph",
		"--session-id", "lifecycle",
		"--tsgo", wrapper,
	)
	cmd.Env = append(os.Environ(),
		"CGRAPH_REAL_TSGO="+realTsgo,
		"CGRAPH_TSGO_PID_FILE="+pidFile,
	)
	cmd.Stderr = stderr
	return cmd, stderr
}

func startLifecycleDaemon(t *testing.T) *daemonProcess {
	t.Helper()
	root := fixtureRoot(t)
	dir := t.TempDir()
	jsonl := filepath.Join(dir, "telemetry.jsonl")
	pidFile := filepath.Join(dir, "tsgo.pid")
	cmd, stderr := makeDaemonCommand(t, root, jsonl, pidFile)
	client := mcp.NewClient(&mcp.Implementation{Name: "lifecycle", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting to daemon: %v (stderr=%s)", err, stderr.String())
	}
	d := &daemonProcess{cmd: cmd, sess: sess, stderr: stderr, tsgoPID: pidFile}
	t.Cleanup(func() { d.cleanup(t) })
	waitForFile(t, pidFile)
	return d
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func (d *daemonProcess) waitForExit(timeout time.Duration) bool {
	if d.waitDone == nil {
		d.waitDone = make(chan error, 1)
		go func() { d.waitDone <- d.cmd.Wait() }()
	}
	select {
	case <-d.waitDone:
		d.waited = true
		return true
	case <-time.After(timeout):
		return false
	}
}

func waitForCommand(t *testing.T, d *daemonProcess) {
	t.Helper()
	if !d.waitForExit(5 * time.Second) {
		t.Fatal("daemon did not exit promptly")
	}
}

func assertChildExited(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading tsgo PID: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		t.Fatalf("invalid tsgo PID %q: %v", data, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			if err == syscall.ESRCH {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tsgo child pid %d did not exit promptly", pid)
}

func (d *daemonProcess) cleanup(t *testing.T) {
	if d.closed {
		return
	}
	d.closed = true

	closeDone := make(chan error, 1)
	go func() { closeDone <- d.sess.Close() }()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Log("MCP session close timed out; forcing daemon termination")
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
	}

	if !d.waitForExit(5 * time.Second) {
		t.Log("daemon did not terminate after session close; forcing daemon termination")
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
		if !d.waitForExit(5 * time.Second) {
			t.Errorf("daemon did not terminate after forced kill")
		}
	}
	assertChildExited(t, d.tsgoPID)
}

// session spawns the real daemon over MCP and returns a connected client
// session plus the telemetry JSONL path it writes to.
func session(t *testing.T) (*mcp.ClientSession, string) {
	t.Helper()
	if _, err := exec.LookPath("tsgo"); err != nil {
		t.Skip("tsgo not on PATH; skipping Tier A (the daemon spawns tsgo --lsp)")
	}
	root := fixtureRoot(t)
	jsonl := filepath.Join(t.TempDir(), "telemetry.jsonl")

	cmd := exec.Command(daemonBin,
		"--project-root", root,
		"--jsonl", jsonl,
		"--graph-mode", "graph",
		"--session-id", "tiera",
	)
	cmd.Stderr = os.Stderr // daemon logs go to the test log

	client := mcp.NewClient(&mcp.Implementation{Name: "tiera", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting to daemon: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, jsonl
}

// callInto calls a tool and decodes its structured output into out.
func callInto(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: call error: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool reported protocol error: %+v", name, res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshaling structured content: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: decoding output: %v (raw=%s)", name, err, raw)
	}
}

func TestTierA(t *testing.T) {
	sess, jsonl := session(t)
	geometry := filepath.Join(fixtureRoot(t), "src", "geometry.ts")
	mainTS := filepath.Join(fixtureRoot(t), "src", "main.ts")

	// The daemon must advertise exactly the three Phase 0 tools.
	t.Run("tools_list", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		lt, err := sess.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		got := map[string]bool{}
		for _, tl := range lt.Tools {
			got[tl.Name] = true
		}
		for _, want := range []string{"find_definition", "find_references", "get_outline"} {
			if !got[want] {
				t.Errorf("tool %q not advertised (got %v)", want, keys(got))
			}
		}
	})

	// get_outline(geometry.ts) must surface the declared top-level symbols.
	t.Run("outline_geometry", func(t *testing.T) {
		var out tools.GetOutlineOutput
		callInto(t, sess, "get_outline", map[string]any{"file": geometry}, &out)
		if !out.Found {
			t.Fatalf("expected symbols, got none: %s", out.Message)
		}
		names := map[string]bool{}
		for _, s := range out.Symbols {
			names[s.Name] = true
		}
		for _, want := range []string{"Shape", "Circle", "Rectangle", "totalArea"} {
			if !names[want] {
				t.Errorf("outline missing %q (got %v)", want, keys(names))
			}
		}
		assertFresh(t, out.Freshness.Stale)
	})

	// find_references(geometry.ts, Circle) must include the cross-file uses in
	// main.ts — the core proof that reference resolution spans files.
	t.Run("references_cross_file", func(t *testing.T) {
		var out tools.FindReferencesOutput
		callInto(t, sess, "find_references", map[string]any{"file": geometry, "symbol": "Circle"}, &out)
		if !out.Found {
			t.Fatalf("expected references to Circle, got none: %s", out.Message)
		}
		var files []string
		crossFile := false
		for _, l := range out.Locations {
			files = append(files, l.File)
			if strings.HasSuffix(filepath.ToSlash(l.File), "src/main.ts") {
				crossFile = true
			}
		}
		if !crossFile {
			t.Errorf("expected a reference in src/main.ts; got files %v", files)
		}
		assertFresh(t, out.Freshness.Stale)
	})

	// find_definition(geometry.ts, totalArea) resolves the declaration to a
	// location back in geometry.ts.
	t.Run("definition_totalArea", func(t *testing.T) {
		var out tools.FindDefinitionOutput
		callInto(t, sess, "find_definition", map[string]any{"file": geometry, "symbol": "totalArea"}, &out)
		if !out.Found {
			t.Fatalf("expected a definition, got none: %s", out.Message)
		}
		if got := out.Locations[0].File; !strings.HasSuffix(filepath.ToSlash(got), "src/geometry.ts") {
			t.Errorf("expected definition in src/geometry.ts, got %s", got)
		}
		assertFresh(t, out.Freshness.Stale)
	})

	// get_outline(main.ts) surfaces the consumer's own declarations.
	t.Run("outline_main", func(t *testing.T) {
		var out tools.GetOutlineOutput
		callInto(t, sess, "get_outline", map[string]any{"file": mainTS}, &out)
		names := map[string]bool{}
		for _, s := range out.Symbols {
			names[s.Name] = true
		}
		for _, want := range []string{"shapes", "report"} {
			if !names[want] {
				t.Errorf("main.ts outline missing %q (got %v)", want, keys(names))
			}
		}
	})

	// Phase 0 exit criterion: every call is logged. After the calls above,
	// the JSONL stream must contain one line per tool invocation.
	t.Run("every_call_logged", func(t *testing.T) {
		// Close the session first so the daemon flushes and exits.
		_ = sess.Close()
		// Give the OS a moment; poll the file.
		var lines []map[string]any
		deadline := time.Now().Add(5 * time.Second)
		// 4 tool calls above (2 outlines + 1 refs + 1 def); ListTools is not a
		// tool call and emits no event. Break as soon as they've all landed.
		for time.Now().Before(deadline) {
			lines = readJSONL(t, jsonl)
			if len(lines) >= 4 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(lines) < 4 {
			t.Fatalf("expected >=4 telemetry events (one per tool call), got %d: %v", len(lines), lines)
		}
		seen := map[string]int{}
		for _, l := range lines {
			if tool, ok := l["tool"].(string); ok {
				seen[tool]++
			}
			if _, ok := l["ts"]; !ok {
				t.Errorf("telemetry event missing timestamp: %v", l)
			}
		}
		for _, want := range []string{"get_outline", "find_references", "find_definition"} {
			if seen[want] == 0 {
				t.Errorf("no telemetry event for tool %q (saw %v)", want, seen)
			}
		}
	})
}

func TestDaemonStdinEOFStopsProvider(t *testing.T) {
	d := startLifecycleDaemon(t)
	started := time.Now()
	if err := d.sess.Close(); err != nil {
		t.Fatalf("closing MCP stdin: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("MCP disconnect took too long: %v", elapsed)
	}
	assertChildExited(t, d.tsgoPID)
	d.closed = true
}

func TestDaemonSIGTERMStopsWithIdleControlClient(t *testing.T) {
	d := startLifecycleDaemon(t)
	sock := cgmcp.SocketPath(core.Config{ProjectRoot: fixtureRoot(t)})
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("connecting idle control client: %v", err)
	}
	defer conn.Close()

	started := time.Now()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	waitForCommand(t, d)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("SIGTERM shutdown took too long: %v", elapsed)
	}
	assertChildExited(t, d.tsgoPID)
	_ = d.sess.Close()
	d.closed = true
}

func TestDuplicateDaemonLeavesOriginalFunctional(t *testing.T) {
	first := startLifecycleDaemon(t)
	dir := t.TempDir()
	secondPID := filepath.Join(dir, "tsgo.pid")
	secondCmd, stderr := makeDaemonCommand(t, fixtureRoot(t), filepath.Join(dir, "telemetry.jsonl"), secondPID)
	secondClient := mcp.NewClient(&mcp.Implementation{Name: "duplicate", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	_, err := secondClient.Connect(ctx, &mcp.CommandTransport{Command: secondCmd}, nil)
	cancel()
	if err == nil {
		t.Fatal("duplicate daemon unexpectedly connected")
	}
	if waitErr := secondCmd.Wait(); waitErr == nil {
		t.Fatal("duplicate daemon unexpectedly exited successfully")
	}
	if !strings.Contains(stderr.String(), "already owned") {
		t.Fatalf("duplicate startup error was not clear: %s", stderr.String())
	}

	listCtx, listCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer listCancel()
	result, err := first.sess.ListTools(listCtx, nil)
	if err != nil || len(result.Tools) != 3 {
		t.Fatalf("original daemon was disrupted: tools=%d err=%v", len(result.Tools), err)
	}
}

func assertFresh(t *testing.T, stale bool) {
	t.Helper()
	if stale {
		t.Errorf("Phase 0 results must never be stale (no barrier yet), got stale=true")
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("malformed JSONL line: %q: %v", line, err)
			continue
		}
		out = append(out, m)
	}
	return out
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
