package main

import (
	"image/color"
	"os"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/oligo/gioview/theme"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

type UI struct {
	window *app.Window
	theme  *theme.Theme
	vm     *HomeView
}

func (ui *UI) Loop() error {
	var ops op.Ops
	for {
		e := ui.window.Event()

		switch e := e.(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			ui.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (ui *UI) layout(gtx C) D {
	if ui.vm == nil {
		ui.vm = newHome(ui.window)
	}

	return ui.vm.Layout(gtx, ui.theme)
}

func main() {
	go func() {
		w := &app.Window{}
		w.Option(app.Title("VOID"), app.Size(unit.Dp(1200), unit.Dp(800)))

		th := theme.NewTheme("./fonts", nil, true)
		th.Palette.Fg = rgb(0xd7dade)
		th.Palette.Bg = rgb(0x303134)
		th.Palette.ContrastBg = rgb(0x616365)
		th.Palette.ContrastFg = rgb(0xffffff)

		th.TextSize = unit.Sp(14)
		th.Bg2 = rgb(0x434548)

		ui := &UI{theme: th, window: w}
		err := ui.Loop()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()

	app.Main()
}

func rgb(c uint32) color.NRGBA {
	return argb(0xff000000 | c)
}
func argb(c uint32) color.NRGBA {
	return color.NRGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}
