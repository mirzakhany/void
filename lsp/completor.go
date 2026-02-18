package lsp

import (
	"context"
	"strings"

	"gioui.org/io/key"
	"github.com/oligo/gvcode"
	"go.lsp.dev/protocol"
)

// Completor adapts an LSP client to gvcode.Completor for one file.
type Completor struct {
	Client     *Client
	DocURI     protocol.DocumentURI
	Editor     *gvcode.Editor
	ProjectRoot string
}

// Trigger implements gvcode.Completor: trigger on "." and on Ctrl+Space.
func (c *Completor) Trigger() gvcode.Trigger {
	return gvcode.Trigger{
		Characters: []string{".", ":"},
		KeyBinding: struct {
			Name      key.Name
			Modifiers key.Modifiers
		}{
			Name: key.NameSpace, Modifiers: key.ModShortcut,
		},
	}
}

// Suggest implements gvcode.Completor by calling LSP textDocument/completion.
func (c *Completor) Suggest(ctx gvcode.CompletionContext) []gvcode.CompletionCandidate {
	if c.Client == nil {
		return nil
	}
	text := c.Editor.Text()
	// gvcode CaretPos() is 0-based line and column (rune-based). LSP expects UTF-16 character offset.
	line := uint32(ctx.Position.Line)
	lineText := getLineAt(text, int(line))
	character := uint32(runeColToUTF16(lineText, ctx.Position.Column))
	lspCtx := context.Background()
	list, err := c.Client.Completion(lspCtx, c.DocURI, line, character, text)
	if err != nil || list == nil {
		return nil
	}
	if list.Items == nil {
		return nil
	}
	candidates := make([]gvcode.CompletionCandidate, 0, len(list.Items))
	for _, item := range list.Items {
		cand := completionItemToCandidate(item, ctx.Position.Runes)
		candidates = append(candidates, cand)
	}
	return candidates
}

// FilterAndRank implements gvcode.Completor: simple prefix filter.
func (c *Completor) FilterAndRank(pattern string, candidates []gvcode.CompletionCandidate) []gvcode.CompletionCandidate {
	if pattern == "" {
		return candidates
	}
	filtered := make([]gvcode.CompletionCandidate, 0)
	lower := strings.ToLower(pattern)
	for _, cand := range candidates {
		if strings.HasPrefix(strings.ToLower(cand.Label), lower) {
			filtered = append(filtered, cand)
		}
	}
	return filtered
}

func completionItemToCandidate(item protocol.CompletionItem, caretRunes int) gvcode.CompletionCandidate {
	label := item.Label
	insertText := label
	if item.InsertText != "" {
		insertText = item.InsertText
	}
	if item.TextEdit != nil {
		insertText = item.TextEdit.NewText
	}
	start, end := caretRunes, caretRunes
	if item.TextEdit != nil && item.TextEdit.Range.Start.Character != item.TextEdit.Range.End.Character {
		// Use edit range if provided (we'd need document text to convert; for now use caret)
		start = caretRunes
		end = caretRunes
	}
	kind := lspKindToString(item.Kind)
	desc := item.Detail
	if desc == "" && item.Documentation != nil {
		if s, ok := item.Documentation.(string); ok {
			desc = s
		}
	}
	textFormat := "PlainText"
	if item.InsertTextFormat == protocol.InsertTextFormatSnippet {
		textFormat = "Snippet"
	}
	return gvcode.CompletionCandidate{
		Label: label,
		TextEdit: gvcode.TextEdit{
			NewText: insertText,
			EditRange: gvcode.EditRange{
				Start: gvcode.Position{Runes: start},
				End:   gvcode.Position{Runes: end},
			},
		},
		Description: desc,
		Kind:        kind,
		TextFormat:  textFormat,
	}
}

// getLineAt returns the line at 0-based index (without trailing newline).
func getLineAt(text string, lineIndex int) string {
	start := 0
	for i := 0; i < lineIndex && start < len(text); i++ {
		idx := 0
		for idx < len(text)-start && text[start+idx] != '\n' {
			idx++
		}
		start += idx + 1
	}
	end := start
	for end < len(text) && text[end] != '\n' {
		end++
	}
	return text[start:end]
}

func lspKindToString(k protocol.CompletionItemKind) string {
	switch k {
	case protocol.CompletionItemKindMethod:
		return "method"
	case protocol.CompletionItemKindFunction:
		return "function"
	case protocol.CompletionItemKindConstructor:
		return "constructor"
	case protocol.CompletionItemKindField:
		return "field"
	case protocol.CompletionItemKindVariable:
		return "variable"
	case protocol.CompletionItemKindClass:
		return "class"
	case protocol.CompletionItemKindInterface:
		return "interface"
	case protocol.CompletionItemKindModule:
		return "module"
	case protocol.CompletionItemKindProperty:
		return "property"
	case protocol.CompletionItemKindKeyword:
		return "keyword"
	case protocol.CompletionItemKindSnippet:
		return "snippet"
	case protocol.CompletionItemKindStruct:
		return "struct"
	case protocol.CompletionItemKindConstant:
		return "constant"
	default:
		return "text"
	}
}
