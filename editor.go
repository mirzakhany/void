package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/chapar-rest/uikit/tabs"
	"github.com/chapar-rest/uikit/theme"
	"github.com/mirzakhany/void/lsp"
	"github.com/oligo/gvcode"
	"github.com/oligo/gvcode/addons/completion"
	gvcolor "github.com/oligo/gvcode/color"
	"github.com/oligo/gvcode/textstyle/decoration"
	"github.com/oligo/gvcode/textstyle/syntax"
	wg "github.com/oligo/gvcode/widget"
	"go.lsp.dev/protocol"
)

// completionWrapper wraps DefaultCompletion so that typing a trigger character (e.g. ".")
// cancels the current session first. That forces a new session and a fresh LSP Suggest()
// call, so we get member completions (e.g. fmt.Println after "fmt.").
type completionWrapper struct {
	*completion.DefaultCompletion
	triggerChars map[string]bool
}

func (w *completionWrapper) OnText(ctx gvcode.CompletionContext) {
	if w.triggerChars[ctx.Input] {
		w.Cancel()
	}
	w.DefaultCompletion.OnText(ctx)
}

// buildFileView creates a fileView for the given path with editor, syntax highlighting, and completion.
func (s *appState) buildFileView(th *theme.Theme, path string) fileView {
	content, err := os.ReadFile(path)
	if err != nil {
		content = []byte(fmt.Sprintf("// Error reading %s: %v", path, err))
	}

	ed := wg.NewEditor(th.Material())
	ed.WithOptions(
		gvcode.WithFont(EditorFont()),
		gvcode.WithLineNumber(true),
		gvcode.WithLineNumberGutterGap(unit.Dp(12)),
		gvcode.WithTextSize(unit.Sp(14)),
		gvcode.WithLineHeight(0, 1.35),
		gvcode.WithTabWidth(4),
	)
	ed.SetText(string(content))

	// Completion: prefer LSP if a server is configured for this file.
	// Use completionWrapper so that typing "." or ":" cancels the current session and starts
	// a new one, causing LSP Suggest() to be called again (e.g. for "fmt." -> Println, Printf).
	defaultComp := &completion.DefaultCompletion{Editor: ed}
	cm := &completionWrapper{
		DefaultCompletion: defaultComp,
		triggerChars:      map[string]bool{".": true, ":": true},
	}
	popup := completion.NewCompletionPopup(ed, cm)
	popup.Theme = th.Material()
	popup.TextSize = unit.Sp(12)
	var lspClient *lsp.Client
	// Use absolute path so document URI matches what gopls sends in publishDiagnostics.
	absPath, _ := filepath.Abs(path)
	docURI := string(lsp.FileURI(absPath))
	projectRoot := "."
	if s.lspManager != nil {
		c, err := s.lspManager.ClientFor(context.Background(), projectRoot, path)
		if err != nil {
			log.Printf("[LSP] failed to start client for %q: %v", path, err)
		}
		if err == nil && c != nil {
			lspClient = c
			log.Printf("[LSP] registered diagnostics handler for %q", path)
			c.RegisterDiagnosticsHandler(docURI, func(diagnostics []protocol.Diagnostic) {
				log.Printf("[LSP] received diagnostics for %q: %v", path, diagnostics)
				s.pendingDiagMu.Lock()
				s.pendingDiag[path] = diagnostics
				s.pendingDiagMu.Unlock()
			})
			if err := c.DidOpen(context.Background(), protocol.DocumentURI(docURI), lspLanguageID(path), 1, string(content)); err != nil {
				log.Printf("[LSP] failed to send didOpen for %q: %v", path, err)
			}
			if err := cm.AddCompletor(&lsp.Completor{Client: c, DocURI: protocol.DocumentURI(docURI), Editor: ed, ProjectRoot: projectRoot}, popup); err != nil {
				log.Printf("[LSP] failed to add completor for %q: %v", path, err)
			}
			log.Printf("[LSP] added completor for %q", path)
		}
	}
	ed.WithOptions(gvcode.WithAutoCompletion(cm))

	// Build color scheme from chroma style and apply syntax highlighting
	chromaStyle := styles.Get("dracula")
	if chromaStyle == nil {
		chromaStyle = styles.Fallback
	}
	gvScheme := buildColorSchemeFromChroma(th.Material(), chromaStyle)
	ed.WithOptions(gvcode.WithColorScheme(gvScheme))

	originalContent := string(content)
	tokens := chromaTokensToGvcode(path, originalContent, chromaStyle)
	if len(tokens) > 0 {
		ed.SetSyntaxTokens(tokens...)
	}

	docVersion := int32(1)
	onChange := func(currentContent string) {
		if tab := s.openTabs[path]; tab != nil {
			if currentContent == originalContent {
				tab.State = tabs.TabStateClean
			} else {
				tab.State = tabs.TabStateDirty
			}
		}
	}

	fv := fileView{
		Title:           path,
		Path:            path,
		Editor:          ed,
		OriginalContent: originalContent,
		OnChange:        onChange,
		LSPClient:       lspClient,
		LSPDocURI:       docURI,
		DocVersion:      docVersion,
		Layout: func(gtx layout.Context, th *theme.Theme) layout.Dimensions {
			// Apply any pending LSP diagnostics (from background callback)
			s.pendingDiagMu.Lock()
			pending := s.pendingDiag[path]
			delete(s.pendingDiag, path)
			s.pendingDiagMu.Unlock()
			if len(pending) > 0 {
				log.Printf("[LSP] applying diagnostics for %q: %v", path, pending)
				applyDiagnostics(ed, pending)
				// Keep a copy for hover tooltip lookup.
				s.currentDiag[path] = make([]protocol.Diagnostic, len(pending))
				copy(s.currentDiag[path], pending)
			}
			for {
				evt, ok := ed.Update(gtx)
				if !ok {
					break
				}
				if _, isChange := evt.(gvcode.ChangeEvent); isChange {
					if onChange != nil {
						onChange(ed.Text())
					}
					if lspClient != nil {
						docVersion++
						text := ed.Text()
						_ = lspClient.DidChange(context.Background(), protocol.DocumentURI(docURI), docVersion, text)
						_ = lspClient.DidSave(context.Background(), protocol.DocumentURI(docURI), text)
					}
					ed.OnTextEdit()
					tokens := chromaTokensToGvcode(path, ed.Text(), chromaStyle)
					if len(tokens) > 0 {
						ed.SetSyntaxTokens(tokens...)
					}
				}
			}
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				dims := ed.Layout(gtx, th.Material().Shaper)
				// Show diagnostic hover when caret is inside an LSP diagnostic range.
				if diag := diagnosticAtCaret(ed, s.currentDiag[path]); diag != nil {
					caret := ed.CaretCoords()
					pos := image.Pt(int(caret.X), int(caret.Y))
					ed.PaintOverlay(gtx, pos, func(gtx layout.Context) layout.Dimensions {
						return layoutDiagnosticTooltip(gtx, th, diag)
					})
				}
				return dims
			})
		},
	}
	return fv
}

// lspLanguageID returns a simple language ID from file extension for LSP.
func lspLanguageID(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	default:
		return "plaintext"
	}
}

// applyDiagnostics converts LSP diagnostics to gvcode decorations (squiggles) and applies them.
func applyDiagnostics(ed *gvcode.Editor, diagnostics []protocol.Diagnostic) {
	text := ed.Text()
	ed.ClearDecorations(lsp.DecorationSource)
	if len(diagnostics) == 0 {
		return
	}
	maxRune := len([]rune(text))
	decos := make([]decoration.Decoration, 0, len(diagnostics))
	for _, d := range diagnostics {
		start, end := lsp.RangeToRuneOffsets(text, d.Range)
		if start < 0 {
			start = 0
		}
		if end > maxRune {
			end = maxRune
		}
		if start >= end {
			// Zero-length range (e.g. line-only diagnostics): underline at least one character so it's visible
			end = start + 1
			if end > maxRune {
				end = maxRune
			}
			if start >= end {
				continue
			}
		}
		c := errorColor
		if d.Severity == protocol.DiagnosticSeverityWarning {
			c = warningColor
		}
		decos = append(decos, decoration.Decoration{
			Source:   lsp.DecorationSource,
			Start:    start,
			End:      end,
			Squiggle: &decoration.Squiggle{Color: gvcolor.MakeColor(c)},
		})
	}
	if len(decos) > 0 {
		if err := ed.AddDecorations(decos...); err != nil {
			log.Printf("[LSP] AddDecorations failed: %v", err)
		}
	}
}

// diagnosticAtCaret returns the first LSP diagnostic whose range contains the editor's caret (by rune offset).
func diagnosticAtCaret(ed *gvcode.Editor, diagnostics []protocol.Diagnostic) *protocol.Diagnostic {
	if len(diagnostics) == 0 {
		return nil
	}
	line, col := ed.CaretPos()
	runeOff, _ := ed.ConvertPos(line, col)
	text := ed.Text()
	maxRune := len([]rune(text))
	for i := range diagnostics {
		d := &diagnostics[i]
		start, end := lsp.RangeToRuneOffsets(text, d.Range)
		if start < 0 {
			start = 0
		}
		if end > maxRune {
			end = maxRune
		}
		if start >= end {
			end = start + 1
		}
		if runeOff >= start && runeOff < end {
			return d
		}
	}
	return nil
}

// layoutDiagnosticTooltip draws a small tooltip with the diagnostic message and optional source/code.
func layoutDiagnosticTooltip(gtx layout.Context, th *theme.Theme, d *protocol.Diagnostic) layout.Dimensions {
	mat := th.Material()
	msg := d.Message
	if msg == "" {
		msg = "(no message)"
	}
	severityLabel := "Error"
	if d.Severity == protocol.DiagnosticSeverityWarning {
		severityLabel = "Warning"
	}
	sub := severityLabel
	if d.Source != "" {
		sub += " · " + string(d.Source)
	}
	if d.Code != nil {
		if d.Source != "" {
			sub += " "
		}
		sub += fmt.Sprintf("%v", d.Code)
	}

	corner := unit.Dp(6)
	maxWidth := gtx.Dp(unit.Dp(320))
	maxHeight := gtx.Dp(unit.Dp(200)) // cap height so tooltip stays compact
	pad := unit.Dp(10)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, maxWidth+gtx.Dp(pad)*2)
	gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, maxHeight)
	gtx.Constraints.Min.Y = 0

	content := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(mat, unit.Sp(12), sub)
				lbl.Color = th.Base.Secondary
				return lbl.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Spacer{Height: unit.Dp(4)}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(mat, unit.Sp(13), msg)
				lbl.Color = mat.Fg
				lbl.MaxLines = 10
				return lbl.Layout(gtx)
			}),
		)
	}

	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(pad).Layout(gtx, content)
	call := macro.Stop()

	rr := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(corner))
	defer rr.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, th.Base.SurfaceHighlight)
	call.Add(gtx.Ops)
	return dims
}

var (
	errorColor   = color.NRGBA{R: 0xf4, G: 0x43, B: 0x36, A: 0xff}
	warningColor = color.NRGBA{R: 0xff, G: 0x98, B: 0x00, A: 0xff}
)

// buildColorSchemeFromChroma creates a gvcode ColorScheme from a chroma style.
func buildColorSchemeFromChroma(mat *material.Theme, chromaStyle *chroma.Style) syntax.ColorScheme {
	cs := syntax.ColorScheme{}
	cs.Foreground = gvcolor.MakeColor(mat.Fg)
	cs.Background = gvcolor.MakeColor(mat.Bg)
	cs.SelectColor = gvcolor.MakeColor(mat.ContrastBg).MulAlpha(0x60)
	cs.LineColor = gvcolor.MakeColor(mat.ContrastBg).MulAlpha(0x30)
	cs.LineNumberColor = gvcolor.MakeColor(mat.Fg).MulAlpha(0xb6)

	for _, tt := range []chroma.TokenType{
		chroma.Keyword, chroma.KeywordConstant, chroma.KeywordDeclaration, chroma.KeywordType,
		chroma.Name, chroma.NameBuiltin, chroma.NameFunction, chroma.NameVariable,
		chroma.LiteralString, chroma.LiteralStringChar, chroma.LiteralStringEscape,
		chroma.LiteralNumber, chroma.LiteralNumberInteger, chroma.LiteralNumberFloat,
		chroma.Comment, chroma.CommentSingle, chroma.CommentMultiline,
		chroma.Operator, chroma.Punctuation,
		chroma.Text, chroma.Whitespace,
	} {
		entry := chromaStyle.Get(tt)
		if entry.Colour.IsSet() {
			fg := gvcolor.MakeColor(color.NRGBA{
				R: entry.Colour.Red(),
				G: entry.Colour.Green(),
				B: entry.Colour.Blue(),
				A: 255,
			})
			cs.AddStyle(syntax.StyleScope(tt.String()), 0, fg, gvcolor.Color{})
		}
	}
	return cs
}

// chromaTokensToGvcode tokenizes content with chroma and returns gvcode syntax tokens.
func chromaTokensToGvcode(filename, content string, _ *chroma.Style) []syntax.Token {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	it, err := lexer.Tokenise(nil, content)
	if err != nil {
		return nil
	}

	var tokens []syntax.Token
	runeOffset := 0
	for t := it(); t != chroma.EOF; t = it() {
		if t.Value == "" {
			continue
		}
		start := runeOffset
		runeOffset += utf8.RuneCountInString(t.Value)
		end := runeOffset
		scope := syntax.StyleScope(t.Type.String())
		if scope.IsValid() {
			tokens = append(tokens, syntax.Token{Start: start, End: end, Scope: scope})
		}
	}
	return tokens
}
