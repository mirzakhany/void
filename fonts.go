package main

import (
	_ "embed"
	"slices"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"github.com/chapar-rest/uikit/theme"
)

//go:embed fonts/JetBrainsMono-Regular.ttf
var jetbrainsMonoTTF []byte

// editorFont is the font descriptor for the embedded JetBrains Mono (set when editorFonts runs).
var editorFont font.Font

// editorFonts returns a font collection with JetBrains Mono Regular first (for the editor),
// then the theme's builtin fonts as fallback.
func editorFonts() []font.FontFace {
	faces, err := opentype.ParseCollection(jetbrainsMonoTTF)
	if err != nil {
		// Fallback to builtin only if embed/parse fails (e.g. missing file).
		return theme.BuiltinFonts()
	}
	if len(faces) > 0 {
		editorFont = faces[0].Font
	}
	out := make([]font.FontFace, 0, len(faces)+len(theme.BuiltinFonts()))
	for _, f := range faces {
		out = append(out, font.FontFace{Font: f.Font, Face: f.Face})
	}
	builtin := theme.BuiltinFonts()
	out = append(out, builtin...)
	return slices.Clip(out)
}

// EditorFont returns the JetBrains Mono font descriptor for use with gvcode.WithFont.
// Must be called after editorFonts() (e.g. after newAppState); otherwise returns zero value.
func EditorFont() font.Font {
	return editorFont
}
