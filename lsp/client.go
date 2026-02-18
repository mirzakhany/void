package lsp

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
)

// DiagnosticsCallback is called when the server sends publishDiagnostics for a document.
type DiagnosticsCallback func(documentURI string, diagnostics []protocol.Diagnostic)

// PerDocumentDiagnosticsHandler is called with diagnostics for a single document (used when one client serves many files).
type PerDocumentDiagnosticsHandler func(diagnostics []protocol.Diagnostic)

// DecorationSource is the source tag used for LSP diagnostics in gvcode decorations.
const DecorationSource = "lsp"

// stdioConn connects Read from process stdout and Write to process stdin.
type stdioConn struct {
	r io.Reader
	w io.Writer
	c io.Closer
}

func (s stdioConn) Read(p []byte) (n int, err error)  { return s.r.Read(p) }
func (s stdioConn) Write(p []byte) (n int, err error) { return s.w.Write(p) }
func (s stdioConn) Close() error {
	if s.c != nil {
		return s.c.Close()
	}
	return nil
}

type multiCloser struct {
	a, b io.Closer
}

func (m *multiCloser) Close() error {
	_ = m.a.Close()
	_ = m.b.Close()
	return nil
}

var _ io.ReadWriteCloser = (*stdioConn)(nil)

// Client wraps an LSP server connection and provides completion and diagnostics.
type Client struct {
	conn      jsonrpc2.Conn
	server    protocol.Server
	diagHandlers map[string]PerDocumentDiagnosticsHandler // URI -> handler
	mu        sync.Mutex
}

// NewClient starts the language server process (command + args), connects via stdio,
// and performs LSP initialize/initialized. rootURI is the workspace root (file URI).
// Register diagnostics handlers per document with RegisterDiagnosticsHandler.
func NewClient(ctx context.Context, rootURI string, command string, args []string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	stream := jsonrpc2.NewStream(stdioConn{
		r: stdout,
		w: stdin,
		c: &multiCloser{stdin, stdout},
	})
	conn := jsonrpc2.NewConn(stream)
	logger := zap.NewNop()
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		logger = zap.NewExample()
	}

	client := &Client{
		conn:         conn,
		server:       protocol.ServerDispatcher(conn, logger),
		diagHandlers: make(map[string]PerDocumentDiagnosticsHandler),
	}
	// Pass our client so server notifications (e.g. publishDiagnostics) call our methods, not the protocol's default client.
	handler := protocol.ClientHandler(client, func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		return reply(ctx, nil, nil)
	})
	conn.Go(ctx, handler)

	initParams := &protocol.InitializeParams{
		ProcessID: int32(os.Getpid()),
		RootURI:   protocol.URI(rootURI),
		Capabilities: protocol.ClientCapabilities{
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Completion: &protocol.CompletionTextDocumentClientCapabilities{
					CompletionItem: &protocol.CompletionTextDocumentClientCapabilitiesItem{
						SnippetSupport:       true,
						InsertReplaceSupport: true,
						DocumentationFormat:  []protocol.MarkupKind{protocol.Markdown, protocol.PlainText},
						DeprecatedSupport:    true,
					},
				},
				PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{
					RelatedInformation: true,
				},
			},
			Workspace: &protocol.WorkspaceClientCapabilities{
				WorkspaceFolders: true,
			},
		},
		ClientInfo: &protocol.ClientInfo{
			Name:    "void",
			Version: "0.1",
		},
	}
	initResult, err := client.server.Initialize(ctx, initParams)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = initResult

	if err := client.conn.Notify(ctx, protocol.MethodInitialized, &protocol.InitializedParams{}); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return client, nil
}

// diagKey returns a canonical key for handler lookup so URIs from the server match our registration.
func diagKey(documentURI string) string {
	u, err := url.ParseRequestURI(documentURI)
	if err != nil {
		return documentURI
	}
	if u.Scheme != "file" {
		return documentURI
	}
	path := u.Path
	if strings.HasPrefix(path, "/") && len(path) >= 3 && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.FromSlash(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

// PublishDiagnostics implements protocol.Client (called when server sends diagnostics).
func (c *Client) PublishDiagnostics(ctx context.Context, params *protocol.PublishDiagnosticsParams) error {
	if params == nil {
		return nil
	}
	key := diagKey(string(params.URI))
	c.mu.Lock()
	fn := c.diagHandlers[key]
	keys := make([]string, 0, len(c.diagHandlers))
	for k := range c.diagHandlers {
		keys = append(keys, k)
	}
	c.mu.Unlock()
	log.Printf("[LSP] PublishDiagnostics: URI=%q key=%q registeredKeys=%v ndiag=%d", params.URI, key, keys, len(params.Diagnostics))
	if fn != nil {
		fn(params.Diagnostics)
	} else {
		log.Printf("[LSP] PublishDiagnostics: no handler for key %q", key)
	}
	return nil
}

// RegisterDiagnosticsHandler registers a handler for diagnostics for the given document URI.
// The URI is normalized so that server notifications (which may use a slightly different
// URI format) still find this handler.
func (c *Client) RegisterDiagnosticsHandler(documentURI string, fn PerDocumentDiagnosticsHandler) {
	key := diagKey(documentURI)
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		delete(c.diagHandlers, key)
	} else {
		c.diagHandlers[key] = fn
	}
}

// UnregisterDiagnosticsHandler removes the diagnostics handler for the given document URI.
func (c *Client) UnregisterDiagnosticsHandler(documentURI string) {
	c.RegisterDiagnosticsHandler(documentURI, nil)
}

// Progress, LogMessage, ShowMessage, etc. - no-op to satisfy protocol.Client.
func (c *Client) Progress(ctx context.Context, params *protocol.ProgressParams) error                     { return nil }
func (c *Client) WorkDoneProgressCreate(ctx context.Context, params *protocol.WorkDoneProgressCreateParams) error { return nil }
func (c *Client) LogMessage(ctx context.Context, params *protocol.LogMessageParams) error               { return nil }
func (c *Client) ShowMessage(ctx context.Context, params *protocol.ShowMessageParams) error             { return nil }
func (c *Client) ShowMessageRequest(ctx context.Context, params *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	return nil, nil
}
func (c *Client) Telemetry(ctx context.Context, params interface{}) error { return nil }
func (c *Client) RegisterCapability(ctx context.Context, params *protocol.RegistrationParams) error {
	return nil
}
func (c *Client) UnregisterCapability(ctx context.Context, params *protocol.UnregistrationParams) error {
	return nil
}
func (c *Client) ApplyEdit(ctx context.Context, params *protocol.ApplyWorkspaceEditParams) (bool, error) {
	return true, nil
}
func (c *Client) WorkspaceFolders(ctx context.Context) ([]protocol.WorkspaceFolder, error) {
	return nil, nil
}
func (c *Client) Configuration(ctx context.Context, params *protocol.ConfigurationParams) ([]interface{}, error) {
	return nil, nil
}

// Completion requests completion at the given position (0-based line and character).
func (c *Client) Completion(ctx context.Context, docURI protocol.DocumentURI, line, character uint32, text string) (*protocol.CompletionList, error) {
	params := &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
			Position:     protocol.Position{Line: line, Character: character},
		},
		Context: &protocol.CompletionContext{
			TriggerKind: protocol.CompletionTriggerKindInvoked,
		},
	}
	return c.server.Completion(ctx, params)
}

// DidOpen sends textDocument/didOpen.
func (c *Client) DidOpen(ctx context.Context, docURI protocol.DocumentURI, languageID string, version int32, text string) error {
	return c.conn.Notify(ctx, protocol.MethodTextDocumentDidOpen, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        docURI,
			LanguageID: protocol.LanguageIdentifier(languageID),
			Version:    version,
			Text:       text,
		},
	})
}

// DidChange sends textDocument/didChange with full document content.
func (c *Client) DidChange(ctx context.Context, docURI protocol.DocumentURI, version int32, text string) error {
	return c.conn.Notify(ctx, protocol.MethodTextDocumentDidChange, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI},
			Version:                version,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{
			{Text: text},
		},
	})
}

// DidSave sends textDocument/didSave so the server runs diagnostics (gopls often only runs on save).
func (c *Client) DidSave(ctx context.Context, docURI protocol.DocumentURI, text string) error {
	return c.conn.Notify(ctx, protocol.MethodTextDocumentDidSave, &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
		Text:         text,
	})
}

// DidClose sends textDocument/didClose.
func (c *Client) DidClose(ctx context.Context, docURI protocol.DocumentURI) error {
	return c.conn.Notify(ctx, protocol.MethodTextDocumentDidClose, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: docURI},
	})
}

// Close closes the connection and the underlying process.
func (c *Client) Close() error {
	return c.conn.Close()
}

// FileURI returns a file:// URI for the given path.
func FileURI(path string) protocol.DocumentURI {
	return protocol.DocumentURI(uri.File(path))
}

// runeColToUTF16 returns the UTF-16 code unit offset for the given rune column in the line.
func runeColToUTF16(line string, runeCol int) int {
	runes := []rune(line)
	if runeCol > len(runes) {
		runeCol = len(runes)
	}
	n := 0
	for _, r := range runes[:runeCol] {
		if r <= 0xFFFF {
			n++
		} else {
			n += 2
		}
	}
	return n
}

// utf16OffsetToRune returns the rune index in the line for the given UTF-16 code unit offset.
func utf16OffsetToRune(line string, utf16Offset int) int {
	runes := []rune(line)
	n := 0
	for i, r := range runes {
		if n >= utf16Offset {
			return i
		}
		if r <= 0xFFFF {
			n++
		} else {
			n += 2
		}
	}
	return len(runes)
}

// PositionToRuneOffset converts LSP line/character (0-based, character is UTF-16) to rune offset in text.
func PositionToRuneOffset(text string, line, character uint32) int {
	lines := splitLines(text)
	if int(line) >= len(lines) {
		return len([]rune(text))
	}
	lineText := lines[line]
	runeCol := utf16OffsetToRune(lineText, int(character))
	// Rune offset = sum of rune lengths of previous lines + runeCol
	offset := 0
	for i := uint32(0); i < line; i++ {
		offset += len([]rune(lines[i])) + 1 // +1 for newline
	}
	return offset + runeCol
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// RangeToRuneOffsets returns start and end rune offsets for the LSP range in text.
func RangeToRuneOffsets(text string, r protocol.Range) (start, end int) {
	start = PositionToRuneOffset(text, r.Start.Line, r.Start.Character)
	end = PositionToRuneOffset(text, r.End.Line, r.End.Character)
	return start, end
}
