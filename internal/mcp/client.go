package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	// OutputSchema is captured as raw JSON so a server that sends a non-object
	// value (or omits it) cannot fail the whole tools/list decode. KajiCode does
	// not use it, but tolerating it mirrors opencode's schema tolerance.
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// initializeResult is the parsed MCP `initialize` response. KajiCode keeps the
// negotiated protocol version (instead of discarding it) and the server's
// optional human-readable instructions, which are surfaced to the model.
type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Instructions    string `json:"instructions"`
	Capabilities    struct {
		Tools *struct {
			ListChanged bool `json:"listChanged"`
		} `json:"tools"`
		Resources *struct{} `json:"resources"`
		Prompts   *struct{} `json:"prompts"`
	} `json:"capabilities"`
}

const (
	// mcpProtocolVersion is the version KajiCode advertises in `initialize`.
	mcpProtocolVersion = "2024-11-05"
)

type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type ToolClient interface {
	ListTools(context.Context) ([]RemoteTool, error)
	CallTool(context.Context, string, map[string]any) (CallToolResult, error)
	Close() error
}

type Client struct {
	server  Server
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *messageReader
	writer  *messageWriter
	mu      sync.Mutex
	closeMu sync.Mutex
	nextID  int

	// init holds the negotiated handshake result (protocol version, server
	// instructions, capabilities). It is written once during initialize before
	// any other call and only read afterwards.
	init initializeResult

	// dispatchMu guards the response-dispatch state shared with the single
	// reader goroutine. It is never held across a blocking read.
	dispatchMu sync.Mutex
	readerOnce sync.Once
	pending    map[int]chan dispatchResult
	readErr    error
	readDone   bool

	// notifyHandlers receive server-initiated notifications (messages with a
	// method and no id). notifyMu guards the slice; handlers are invoked from the
	// reader goroutine and must not block.
	notifyMu       sync.Mutex
	notifyHandlers []func(method string, params json.RawMessage)

	// progress tracks in-flight tool-call timers so a notifications/progress
	// message resets the matching deadline.
	progress progressRegistry
}

// OnNotification registers a callback invoked for each server-initiated
// notification. Callbacks run on the reader goroutine, so they must return
// quickly (dispatch heavy work to a goroutine). Safe to call before or after
// I/O begins.
func (client *Client) OnNotification(handler func(method string, params json.RawMessage)) {
	if handler == nil {
		return
	}
	client.notifyMu.Lock()
	client.notifyHandlers = append(client.notifyHandlers, handler)
	client.notifyMu.Unlock()
}

func (client *Client) dispatchNotification(method string, params json.RawMessage) {
	if method == progressMethod {
		client.progress.touchProgress(progressTokenFromParams(params))
	}
	client.notifyMu.Lock()
	handlers := append([]func(string, json.RawMessage){}, client.notifyHandlers...)
	client.notifyMu.Unlock()
	for _, handler := range handlers {
		handler(method, params)
	}
}

// dispatchResult carries one matched JSON-RPC response (or a terminal reader
// error) to a waiting caller.
type dispatchResult struct {
	message rpcMessage
	err     error
}

const stdioCloseWaitTimeout = 500 * time.Millisecond

// defaultToolCallTimeout bounds ONE MCP tool call when the caller's context
// carries no deadline. The agent loop's ctx is deadline-free, so without this a
// hung server froze the whole turn silently until the user pressed Esc. 120s
// stays generous for slow-but-real tools (browser automation, big searches);
// per-server overrides can raise it later via config if a real workload needs
// more. A deadline already on ctx (tests, callers with their own budget) wins.
// Var (not const) so tests can shrink it instead of waiting two minutes.
var defaultToolCallTimeout = 120 * time.Second

const (
	// initializeTimeout bounds the MCP handshake so a non-responsive peer fails
	// fast instead of hanging startup.
	initializeTimeout = 30 * time.Second
)

// clientRequestTimeout returns the configured per-server timeout when set,
// otherwise the supplied default. Servers may override connect/list/call timeouts
// via `timeout` (milliseconds) in config.
func clientRequestTimeout(server Server, fallback time.Duration) time.Duration {
	if server.Timeout > 0 {
		return server.Timeout
	}
	return fallback
}

func Connect(ctx context.Context, server Server) (ToolClient, error) {
	switch server.Type {
	case ServerTypeStdio:
		return connectStdio(ctx, server)
	case ServerTypeHTTP:
		// A remote "http" server is really "modern Streamable HTTP with graceful
		// SSE fallback" (opencode does the same). Try Streamable HTTP first; if the
		// server only speaks the older HTTP+SSE transport, initialize fails and we
		// retry over SSE so the user does not have to know which one it is. A
		// transport-agnostic auth failure (401/needs-auth) must NOT be masked by the
		// retry: it is returned as-is so the needs-auth classification still works.
		client, err := connectNetwork(ctx, server)
		if err == nil {
			return client, nil
		}
		if needsAuth(err) || isTransportAuthError(err) {
			return nil, err
		}
		fallback, sseErr := connectRemoteSSE(ctx, server)
		if sseErr == nil {
			return fallback, nil
		}
		// Report the streamable-HTTP error (the transport we tried first) so the
		// message matches what the user configured, but note the SSE attempt.
		return nil, fmt.Errorf("%w (SSE fallback also failed: %v)", err, sseErr)
	case ServerTypeSSE:
		return connectRemoteSSE(ctx, server)
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q for server %s", server.Type, server.Name)
	}
}

// isTransportAuthError reports whether an initialize error looks like an auth
// challenge (401/403) rather than a transport mismatch. Falling back from
// Streamable HTTP to SSE on an auth failure would only repeat the same rejected
// request, and would obscure the needs-auth signal, so we keep the error.
func isTransportAuthError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "401") || strings.Contains(message, "403") || strings.Contains(message, "unauthorized")
}

// maxStderrCapture bounds how much of an MCP server's stderr is retained. The
// buffer is only read when initialize fails (early in the process life), so a
// modest head is plenty; this stops a long-lived, chatty server from growing the
// buffer without bound for the whole process lifetime.
const maxStderrCapture = 64 * 1024

// boundedBuffer is a concurrency-safe io.Writer that retains at most cap bytes
// (the earliest ones) and silently discards the rest, so attaching it as
// cmd.Stderr can never leak unbounded memory. os/exec writes to it from its own
// copy goroutine, hence the mutex.
type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := b.cap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	// Report the full length so the writer never sees a short write.
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func connectStdio(ctx context.Context, server Server) (*Client, error) {
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Env = mergeProcessEnv(server.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdin for %s: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdout for %s: %w", server.Name, err)
	}
	stderr := &boundedBuffer{cap: maxStderrCapture}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %s: %w", server.Name, err)
	}

	client := &Client{
		server: server,
		cmd:    cmd,
		stdin:  stdin,
		reader: newMessageReader(stdout),
		writer: newMessageWriter(stdin),
		nextID: 1,
	}
	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("initialize MCP server %s: %w: %s", server.Name, err, message)
		}
		return nil, fmt.Errorf("initialize MCP server %s: %w", server.Name, err)
	}
	return client, nil
}

// initialize performs the MCP handshake under a bounded timeout so a
// non-responsive peer fails fast instead of hanging startup. It records the
// negotiated protocol version, server instructions, and capabilities.
func (client *Client) initialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, clientRequestTimeout(client.server, initializeTimeout))
	defer cancel()

	var result initializeResult
	if err := client.request(initCtx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "kajicode",
			"version": "dev",
		},
	}, &result); err != nil {
		return err
	}
	client.init = result
	return client.notify("notifications/initialized", map[string]any{})
}

// Instructions returns the server-provided instructions (empty when none).
func (client *Client) Instructions() string {
	return strings.TrimSpace(client.init.Instructions)
}

// NegotiatedProtocolVersion returns the protocol version the server selected.
func (client *Client) NegotiatedProtocolVersion() string {
	return strings.TrimSpace(client.init.ProtocolVersion)
}

// SupportsResources reports whether the server advertised the resources capability.
func (client *Client) SupportsResources() bool {
	return client.init.Capabilities.Resources != nil
}

// SupportsPrompts reports whether the server advertised the prompts capability.
func (client *Client) SupportsPrompts() bool {
	return client.init.Capabilities.Prompts != nil
}

func (client *Client) ListTools(ctx context.Context) ([]RemoteTool, error) {
	return clientListTools(ctx, client.request)
}

func (client *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallToolResult, error) {
	var result CallToolResult
	// Default deadline: a hung MCP server previously waited on ctx forever (the
	// agent loop's ctx has no deadline), silently freezing the turn. The timeout
	// returns a readable error the MODEL sees and can react to — the turn is not
	// killed, only this tool call fails like any other tool error. A per-server
	// `timeout` overrides the built-in default. The deadline resets on each
	// notifications/progress message so a legitimately long call is not killed.
	timeout := clientRequestTimeout(client.server, defaultToolCallTimeout)
	callCtx := ctx
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	var progress *progressTimeout
	var cancel context.CancelCauseFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		callCtx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
		token := nextProgressToken()
		params["_meta"] = map[string]any{"progressToken": token}
		progress = newProgressTimeout(timeout, cancel)
		client.progress.addProgress(token, progress)
		defer func() {
			progress.stop()
			client.progress.removeProgress(token)
		}()
	}
	if err := client.request(callCtx, "tools/call", params, &result); err != nil {
		if cause := context.Cause(callCtx); cause != nil {
			return CallToolResult{}, cause
		}
		return CallToolResult{}, err
	}
	return result, nil
}

func (client *Client) Close() error {
	client.closeMu.Lock()
	defer client.closeMu.Unlock()

	// Fail any callers still waiting on a response. The blocking read in the
	// reader goroutine is released below when stdin closes and the process
	// exits (or is killed), EOFing stdout.
	client.failAll(errors.New("MCP client closed"))

	var err error
	stdin := client.stdin
	cmd := client.cmd
	client.stdin = nil
	client.cmd = nil

	if stdin != nil {
		err = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		select {
		case waitErr := <-waitDone:
			if err == nil && waitErr != nil {
				err = waitErr
			}
		case <-time.After(stdioCloseWaitTimeout):
			killed := false
			killErr := cmd.Process.Kill()
			if killErr == nil {
				killed = true
			} else if err == nil && !errors.Is(killErr, os.ErrProcessDone) {
				err = killErr
			}
			waitErr := <-waitDone
			if err == nil && waitErr != nil && !killed {
				err = waitErr
			}
		}
	}
	return err
}

func (client *Client) request(ctx context.Context, method string, params any, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	client.ensureReader()

	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}

	// Allocate an id, register a response channel, and write the request while
	// holding client.mu. The mutex serializes writes and id allocation but is
	// released before the (potentially unbounded) wait for the response, so a
	// hung server never holds the lock and blocks other callers/Close.
	client.mu.Lock()
	id := client.nextID
	client.nextID++

	responses := make(chan dispatchResult, 1)
	client.dispatchMu.Lock()
	if client.readDone {
		readErr := client.readErr
		client.dispatchMu.Unlock()
		client.mu.Unlock()
		if readErr != nil {
			return readErr
		}
		return fmt.Errorf("MCP %s failed: connection closed", method)
	}
	client.pending[id] = responses
	client.dispatchMu.Unlock()

	if err := client.writer.write(rpcMessage{
		ID:     id,
		Method: method,
		Params: rawParams,
	}); err != nil {
		client.removePending(id)
		// The deadline/cancellation may have fired while the write was stuck
		// (a full pipe blocks forever). Prefer the caller's ctx error so a
		// timed-out call surfaces as DeadlineExceeded, not a write error.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		client.mu.Unlock()
		return err
	}
	client.mu.Unlock()

	select {
	case <-ctx.Done():
		client.removePending(id)
		return ctx.Err()
	case result := <-responses:
		if result.err != nil {
			return result.err
		}
		message := result.message
		if message.Error != nil {
			return fmt.Errorf("MCP %s failed: %s", method, message.Error.Message)
		}
		if target != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, target); err != nil {
				return fmt.Errorf("decode MCP %s result: %w", method, err)
			}
		}
		return nil
	}
}

// ensureReader lazily starts the single reader goroutine. It runs once per
// client; subsequent calls are no-ops.
func (client *Client) ensureReader() {
	client.readerOnce.Do(func() {
		client.dispatchMu.Lock()
		if client.pending == nil {
			client.pending = make(map[int]chan dispatchResult)
		}
		client.dispatchMu.Unlock()
		go client.readLoop()
	})
}

// readLoop is the single consumer of the message reader. It dispatches each
// response to the waiting caller by id and, on a terminal read error (EOF,
// closed pipe, or a Close-triggered cancel), fails all pending callers so none
// block forever.
func (client *Client) readLoop() {
	for {
		message, err := client.reader.read()
		if err != nil {
			client.failAll(err)
			return
		}
		if message.ID == nil {
			// Server-initiated notification: no id, but it carries a method. Hand
			// it to the registered handlers (e.g. tools/list_changed) instead of
			// silently dropping it.
			if message.Method != "" {
				client.dispatchNotification(message.Method, message.Params)
			}
			continue
		}
		id, ok := rpcMessageID(message.ID)
		if !ok {
			continue
		}
		client.dispatchMu.Lock()
		responses := client.pending[id]
		if responses != nil {
			delete(client.pending, id)
		}
		client.dispatchMu.Unlock()
		if responses != nil {
			responses <- dispatchResult{message: message}
		}
	}
}

func (client *Client) removePending(id int) {
	client.dispatchMu.Lock()
	delete(client.pending, id)
	client.dispatchMu.Unlock()
}

func (client *Client) failAll(err error) {
	client.dispatchMu.Lock()
	if client.readDone {
		client.dispatchMu.Unlock()
		return
	}
	client.readDone = true
	client.readErr = err
	pending := client.pending
	client.pending = make(map[int]chan dispatchResult)
	client.dispatchMu.Unlock()
	for _, responses := range pending {
		responses <- dispatchResult{err: err}
	}
}

// rpcMessageID extracts the integer id from a JSON-RPC id value across the
// numeric/string encodings a server may use.
func rpcMessageID(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func rpcIDMatches(value any, id int) bool {
	switch typed := value.(type) {
	case int:
		return typed == id
	case int64:
		return typed == int64(id)
	case float64:
		return typed == float64(id)
	case json.Number:
		parsed, err := typed.Int64()
		return err == nil && parsed == int64(id)
	case string:
		return typed == strconv.Itoa(id)
	default:
		return false
	}
}

func (client *Client) notify(method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return client.writer.write(rpcMessage{
		Method: method,
		Params: rawParams,
	})
}

func mergeProcessEnv(env map[string]string) []string {
	merged := append([]string{}, os.Environ()...)
	for key, value := range env {
		merged = append(merged, key+"="+value)
	}
	return merged
}

func TextContent(content []Content) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
