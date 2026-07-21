package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// readFrame reads one LSP message off r: a block of "Key: Value\r\n" headers
// terminated by a blank line, followed by exactly Content-Length bytes of
// JSON body.
func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q: %w", val, err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, errors.New("lsp: message missing Content-Length header")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeMessage marshals v and writes it to w framed with a Content-Length
// header, under writeMu so concurrent callers don't interleave frames.
func (p *Provider) writeMessage(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("lsp: marshaling message: %w", err)
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := fmt.Fprintf(p.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = p.stdin.Write(data)
	return err
}

// call sends a JSON-RPC request and blocks until a matching response arrives,
// ctx is done, or the per-request timeout elapses. It never hangs the caller
// past that bound.
func (p *Provider) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.pendingMu.Lock()
	if p.closed {
		err := p.connErr
		p.pendingMu.Unlock()
		if err == nil {
			err = errors.New("lsp: provider closed")
		}
		return nil, err
	}
	id := p.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan *jsonrpcMessage, 1)
	p.pending[key] = ch
	p.pendingMu.Unlock()

	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, key)
		p.pendingMu.Unlock()
	}()

	if err := p.writeMessage(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("lsp: writing request %s: %w", method, err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	select {
	case msg, ok := <-ch:
		if !ok || msg == nil {
			p.pendingMu.Lock()
			cerr := p.connErr
			p.pendingMu.Unlock()
			if cerr == nil {
				cerr = errors.New("lsp: connection closed")
			}
			return nil, fmt.Errorf("lsp: %s: %w", method, cerr)
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("lsp: %s: server error %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		return msg.Result, nil
	case <-reqCtx.Done():
		return nil, fmt.Errorf("lsp: %s: %w", method, reqCtx.Err())
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (p *Provider) notify(method string, params any) error {
	p.pendingMu.Lock()
	closed := p.closed
	cerr := p.connErr
	p.pendingMu.Unlock()
	if closed {
		if cerr != nil {
			return fmt.Errorf("lsp: %s: %w", method, cerr)
		}
		return fmt.Errorf("lsp: %s: provider closed", method)
	}
	return p.writeMessage(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// readLoop is the single background reader: it demuxes incoming frames by
// JSON-RPC id into the pending map's per-request channels. It runs for the
// lifetime of the subprocess and exits (marking the provider closed) on any
// read/decode error, most commonly EOF when the process exits.
func (p *Provider) readLoop() {
	for {
		data, err := readFrame(p.stdoutR)
		if err != nil {
			extra := p.stderrBuf.String()
			cause := fmt.Errorf("lsp: connection closed: %w", err)
			if extra != "" {
				cause = fmt.Errorf("%w (stderr: %s)", cause, extra)
			}
			p.shutdownPending(cause)
			return
		}

		var msg jsonrpcMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // malformed frame; skip rather than crash the loop
		}

		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			// Server-initiated request (e.g. tsgo's
			// client/registerCapability, sent with a *string* id like
			// "ts1"). We don't implement any of these — reply
			// MethodNotFound so tsgo isn't left waiting on a response that
			// never comes, which would otherwise stall its request queue.
			p.respondMethodNotFound(msg.ID, msg.Method)
		case msg.Method != "":
			// Notification from the server (e.g. window/logMessage,
			// textDocument/publishDiagnostics). Nothing to do with it yet.
		case len(msg.ID) > 0:
			key := string(msg.ID)
			p.pendingMu.Lock()
			ch, ok := p.pending[key]
			p.pendingMu.Unlock()
			if ok {
				m := msg
				ch <- &m
			}
		}
	}
}

func (p *Provider) respondMethodNotFound(id json.RawMessage, method string) {
	_ = p.writeMessage(rpcErrorResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", method)},
	})
}

// shutdownPending marks the provider closed and releases every goroutine
// currently blocked in call() with cerr.
func (p *Provider) shutdownPending(cerr error) {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.connErr = cerr
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
}

// stderrBuffer captures the subprocess's recent stderr output so transport
// errors (e.g. tsgo crashing) can be reported with useful context.
type stderrBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrBufferCap = 8192

func newStderrBuffer() *stderrBuffer { return &stderrBuffer{} }

func (b *stderrBuffer) drain(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b.mu.Lock()
			b.buf = append(b.buf, buf[:n]...)
			if len(b.buf) > stderrBufferCap {
				b.buf = b.buf[len(b.buf)-stderrBufferCap:]
			}
			b.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (b *stderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}
