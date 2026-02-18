package main

import (
	"strings"
	"unicode"

	"gioui.org/io/key"
	"github.com/oligo/gvcode"
)

// projectCompletor suggests completions from the project index and member index.
type projectCompletor struct {
	editor      *gvcode.Editor
	index       []string
	memberIndex map[string][]string
}

func isSymbolSeparator(ch rune) bool {
	return !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_')
}

func (c *projectCompletor) Trigger() gvcode.Trigger {
	return gvcode.Trigger{
		Characters: []string{"."},
		KeyBinding: struct {
			Name      key.Name
			Modifiers key.Modifiers
		}{
			Name: key.NameSpace, Modifiers: key.ModShortcut,
		},
	}
}

func (c *projectCompletor) textBeforeCaret(runePos int) []rune {
	text := c.editor.Text()
	runes := []rune(text)
	if runePos <= 0 {
		return nil
	}
	if runePos > len(runes) {
		runePos = len(runes)
	}
	return runes[:runePos]
}

func (c *projectCompletor) Suggest(ctx gvcode.CompletionContext) []gvcode.CompletionCandidate {
	before := c.textBeforeCaret(ctx.Position.Runes)
	if len(before) == 0 {
		return nil
	}
	lastDot := -1
	for i := len(before) - 1; i >= 0; i-- {
		if before[i] == '.' {
			lastDot = i
			break
		}
	}
	if lastDot >= 0 && c.memberIndex != nil {
		receiver := string(trimIdentifierRight(before[:lastDot]))
		memberPrefix := string(trimIdentifierLeft(before[lastDot+1:]))
		if list, ok := c.memberIndex[receiver]; ok {
			candidates := make([]gvcode.CompletionCandidate, 0)
			for _, m := range list {
				if strings.HasPrefix(m, memberPrefix) {
					candidates = append(candidates, gvcode.CompletionCandidate{
						Label: m,
						TextEdit: gvcode.TextEdit{
							NewText: m,
						},
						Description: receiver + " member",
						Kind:        "property",
						TextFormat:  "PlainText",
					})
				}
			}
			if len(candidates) > 0 {
				return candidates
			}
		}
	}
	prefix := c.editor.ReadUntil(-1, isSymbolSeparator)
	candidates := make([]gvcode.CompletionCandidate, 0)
	for _, w := range c.index {
		if strings.HasPrefix(w, prefix) {
			candidates = append(candidates, gvcode.CompletionCandidate{
				Label: w,
				TextEdit: gvcode.TextEdit{
					NewText: w,
				},
				Description: "project",
				Kind:        "text",
				TextFormat:  "PlainText",
			})
		}
	}
	return candidates
}

func trimIdentifierRight(r []rune) []rune {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsLetter(r[i]) || r[i] == '_' || unicode.IsDigit(r[i]) {
			return r[:i+1]
		}
	}
	return nil
}

func trimIdentifierLeft(r []rune) []rune {
	for i, x := range r {
		if unicode.IsLetter(x) || x == '_' || unicode.IsDigit(x) {
			return r[i:]
		}
	}
	return nil
}

func (c *projectCompletor) FilterAndRank(pattern string, candidates []gvcode.CompletionCandidate) []gvcode.CompletionCandidate {
	if pattern == "" {
		return candidates
	}
	filtered := make([]gvcode.CompletionCandidate, 0)
	for _, cand := range candidates {
		if strings.HasPrefix(strings.ToLower(cand.Label), strings.ToLower(pattern)) {
			filtered = append(filtered, cand)
		}
	}
	return filtered
}
