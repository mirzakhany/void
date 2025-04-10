package main

import (
	"fmt"
	"image/color"
	"os"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/oligo/gioview/theme"
	"github.com/oligo/gioview/view"
	"github.com/oligo/gvcode"
	wg "github.com/oligo/gvcode/widget"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	EditorViewID = view.NewViewID("EditorView")
)

const (
	syntaxPattern = "package|import|type|func|struct|for|var|switch|case|if|else"
)

type EditorView struct {
	*view.BaseView

	name string
	path string

	state *gvcode.Editor

	lexer     chroma.Lexer
	codeStyle *chroma.Style

	lang string
}

func (vw *EditorView) ID() view.ViewID {
	return EditorViewID
}

func (vw *EditorView) Title() string {
	if vw.name != "" {
		return vw.name
	}

	return "Untitled"
}

func (vw *EditorView) OnNavTo(intent view.Intent) error {
	if err := vw.BaseView.OnNavTo(intent); err != nil {
		return err
	}

	fmt.Println("EditorView.OnNavTo", intent)

	if name, ok := intent.Params["name"]; ok {
		vw.name = name.(string)

		if vw.lexer == nil {
			vw.lexer = getLexer(vw.name)
		}
	}

	if path, ok := intent.Params["path"]; ok {
		vw.path = path.(string)
		thisFile, _ := os.ReadFile(vw.path)
		vw.state.SetText(string(thisFile))
		vw.state.UpdateTextStyles(vw.HightlightTextByPattern(vw.state.Text(), syntaxPattern))
	}

	return nil
}

func (vw *EditorView) Layout(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	for {
		evt, ok := vw.state.Update(gtx)
		if !ok {
			break
		}

		switch evt.(type) {
		case gvcode.ChangeEvent:
			styles := vw.HightlightTextByPattern(vw.state.Text(), syntaxPattern)
			vw.state.UpdateTextStyles(styles)
		}
	}

	return layout.Flex{
		Axis: layout.Vertical,
	}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			borderColor := color.NRGBA{R: 50, G: 52, B: 56, A: 0xab}
			return widget.Border{
				Color: borderColor, Width: unit.Dp(1),
			}.Layout(gtx, func(gtx C) D {
				return layout.Inset{
					Top:    unit.Dp(6),
					Bottom: unit.Dp(6),
					Left:   unit.Dp(24),
					Right:  unit.Dp(24),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					es := wg.NewEditor(th.Theme, vw.state)
					es.Font.Typeface = "Source Code Pro"
					es.TextSize = unit.Sp(14)
					es.LineHeightScale = 1.5
					es.TextHighlightColor = color.NRGBA{R: 120, G: 120, B: 120, A: 200}

					return es.Layout(gtx)
				})
			})
		}),
		layout.Rigid(func(gtx C) D {
			line, col := vw.state.CaretPos()
			lb := material.Label(th.Theme, th.TextSize*0.8, fmt.Sprintf("Line:%d, Col:%d ", line+1, col+1))
			lb.Alignment = text.End
			return lb.Layout(gtx)
		}),
	)
}

func (va *EditorView) OnFinish() {
	va.BaseView.OnFinish()
	// Put your cleanup code here.
}

func NewEditorView() view.View {
	v := &EditorView{
		BaseView: &view.BaseView{},
	}

	style := styles.Get("dracula")
	if style == nil {
		style = styles.Fallback
	}

	v.codeStyle = style

	v.state = &gvcode.Editor{}
	v.state.WithOptions(
		gvcode.WrapLine(true),
	)

	var quotePairs = map[rune]rune{
		'\'': '\'',
		'"':  '"',
		'`':  '`',
		'“':  '”',
	}

	// Bracket pairs
	var bracketPairs = map[rune]rune{
		'(': ')',
		'{': '}',
		'[': ']',
		'<': '>',
	}

	v.state.WithOptions(
		gvcode.WithSoftTab(true),
		gvcode.WithQuotePairs(quotePairs),
		gvcode.WithBracketPairs(bracketPairs),
	)

	return v
}

func (vw *EditorView) HightlightTextByPattern(text string, pattern string) []*gvcode.TextStyle {
	// nolint:prealloc
	var textStyles []*gvcode.TextStyle

	offset := 0

	iterator, err := vw.lexer.Tokenise(nil, text)
	if err != nil {
		return textStyles
	}

	for _, token := range iterator.Tokens() {
		entry := vw.codeStyle.Get(token.Type)

		textStyle := &gvcode.TextStyle{
			TextRange: gvcode.TextRange{
				Start: offset,
				End:   offset + len([]rune(token.Value)),
			},
			Color: rgbaToOp(color.NRGBA{}),
			// Background: rgbToOp(c.theme.Bg),
		}

		if entry.Colour.IsSet() {
			textStyle.Color = chromaColorToOp(entry.Colour)
		}

		textStyles = append(textStyles, textStyle)
		offset = textStyle.End
	}

	return textStyles
}

func rgbaToOp(textColor color.NRGBA) op.CallOp {
	ops := new(op.Ops)

	m := op.Record(ops)
	paint.ColorOp{Color: textColor}.Add(ops)
	return m.Stop()
}

func chromaColorToOp(textColor chroma.Colour) op.CallOp {
	ops := new(op.Ops)

	m := op.Record(ops)
	paint.ColorOp{Color: color.NRGBA{
		R: textColor.Red(),
		G: textColor.Green(),
		B: textColor.Blue(),
		A: 0xff,
	}}.Add(ops)
	return m.Stop()
}

func getLexer(filename string) chroma.Lexer {
	if filename == "" {
		return lexers.Fallback
	}

	if lexer := lexers.Match(filename); lexer != nil {
		return chroma.Coalesce(lexer)
	}

	return lexers.Fallback
}

func detectFromFileName(fileName string) string {
	if fileName == "" {
		return ""
	}

	return ""
}
