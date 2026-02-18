package main

import (
	"os"
	"sync"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/chapar-rest/uikit/actionbar"
	"github.com/chapar-rest/uikit/button"
	"github.com/chapar-rest/uikit/divider"
	"github.com/chapar-rest/uikit/icons"
	"github.com/chapar-rest/uikit/sidebar"
	"github.com/chapar-rest/uikit/split"
	"github.com/chapar-rest/uikit/tabs"
	"github.com/chapar-rest/uikit/theme"
	"github.com/chapar-rest/uikit/theme/themes"
	"github.com/chapar-rest/uikit/treeview"
	"github.com/mirzakhany/void/lsp"
	"github.com/oligo/gvcode"
	"go.lsp.dev/protocol"
)

// appState holds the application's UI state and configuration.
type appState struct {
	SearchClickable   widget.Clickable
	NewFileClickable  widget.Clickable
	OpenFileClickable widget.Clickable
	HistoryClickable  widget.Clickable

	sidebar *sidebar.Sidebar
	split   *split.Split
	tree    *treeview.Tree

	theme     *theme.Theme
	tabitems  *tabs.Tabs
	actionbar *actionbar.ActionBar
	appBar    *actionbar.ActionBar

	openFiles     map[string]fileView
	openTabs      map[string]*tabs.Tab
	openPaths     []string             // path order matching tab order
	tabToPath     map[*tabs.Tab]string // tab -> path for close callback
	lspManager    *lsp.Manager
	pendingDiag   map[string][]protocol.Diagnostic // path -> diagnostics to apply (set by LSP callback)
	pendingDiagMu  sync.Mutex
	currentDiag   map[string][]protocol.Diagnostic // path -> last applied diagnostics (for hover tooltip)
}

// fileView represents an open file in the editor.
type fileView struct {
	Title           string
	Path            string
	Editor          *gvcode.Editor
	OriginalContent string
	OnChange        func(currentContent string)
	Layout          func(gtx layout.Context, th *theme.Theme) layout.Dimensions
	// LSP state (nil if no LSP server for this file)
	LSPClient  *lsp.Client
	LSPDocURI  string
	DocVersion int32
}

// newAppState creates and initializes the application state.
func newAppState() *appState {
	th := themes.Dracula()
	fonts := editorFonts()
	th.WithFonts(fonts)

	state := &appState{
		split: &split.Split{
			Axis:  layout.Horizontal,
			Ratio: 0.3,
			HandleStyle: split.HandleStyle{
				Color:      th.Base.Border,
				Width:      unit.Dp(2),
				HoverColor: th.Base.Secondary,
			},
		},
		sidebar:   sidebar.New(),
		actionbar: actionbar.NewActionBar(layout.Horizontal, layout.Start, layout.SpaceAround),
		appBar:    actionbar.NewActionBar(layout.Horizontal, layout.Start, layout.SpaceBetween),
		theme:     th,
		openFiles: make(map[string]fileView),
		openTabs:  make(map[string]*tabs.Tab),
		openPaths: make([]string, 0),
		tabToPath: make(map[*tabs.Tab]string),
	}
	state.tree = state.buildFileTree(th)
	state.tabitems = tabs.NewTabs()
	state.lspManager = lsp.NewManager(lsp.LoadConfig("."))
	state.pendingDiag = make(map[string][]protocol.Diagnostic)
	state.currentDiag = make(map[string][]protocol.Diagnostic)

	// Action bar buttons
	state.actionbar.AddItem(button.IconButton(state.theme, &state.NewFileClickable, icons.FileAdd, theme.KindPrimary))
	state.actionbar.AddItem(button.IconButton(state.theme, &state.SearchClickable, icons.Search, theme.KindPrimary))
	state.actionbar.AddItem(button.IconButton(state.theme, &state.OpenFileClickable, icons.FileInput, theme.KindPrimary))
	state.actionbar.AddItem(button.IconButton(state.theme, &state.HistoryClickable, icons.History, theme.KindPrimary))

	// App bar
	state.appBar.AddItem(actionbar.ActionBarItemFunc(func(gtx layout.Context, th *theme.Theme) layout.Dimensions {
		return material.Label(th.Material(), unit.Sp(14), "VOID editor").Layout(gtx)
	}))

	// Sidebar nav
	state.sidebar.AddNavItem(sidebar.Item{Tag: "files", Name: "Files", Icon: icons.Files})
	state.sidebar.AddNavItem(sidebar.Item{Tag: "setting", Name: "Setting", Icon: icons.Settings})

	return state
}

// appLayout renders the main application layout.
func (s *appState) appLayout(gtx layout.Context) {
	th := s.theme
	paint.Fill(gtx.Ops, th.Base.Surface)

	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(12), Left: unit.Dp(8), Right: unit.Dp(8), Bottom: unit.Dp(12),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return s.appBar.Layout(gtx, th)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return divider.NewDivider(layout.Horizontal, unit.Dp(1), th.Base.SurfaceHighlight).Layout(gtx, th)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.X = gtx.Dp(68)
					return s.sidebar.Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return divider.NewDivider(layout.Vertical, unit.Dp(1), th.Base.SurfaceHighlight).Layout(gtx, th)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return s.split.Layout(gtx, th,
						s.layoutLeftPanel,
						s.layoutRightPanel,
					)
				}),
			)
		}),
	)
}

func (s *appState) layoutLeftPanel(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return s.actionbar.Layout(gtx, s.theme)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return divider.NewDivider(layout.Horizontal, unit.Dp(1), s.theme.Base.SurfaceHighlight).Layout(gtx, s.theme)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return s.tree.Layout(gtx, s.theme)
		}),
	)
}

func (s *appState) layoutRightPanel(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.tabitems.Layout(gtx, s.theme)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if s.tabitems.CurrentView() < 0 || s.tabitems.CurrentView() >= len(s.openPaths) {
				return layout.Dimensions{}
			}
			path := s.openPaths[s.tabitems.CurrentView()]
			fv, ok := s.openFiles[path]
			if !ok {
				return layout.Dimensions{}
			}
			return fv.Layout(gtx, s.theme)
		}),
	)
}

// runApp starts the main application loop.
func runApp(w *app.Window) error {
	state := newAppState()

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			state.appLayout(gtx)
			e.Frame(gtx.Ops)
		case app.DestroyEvent:
			os.Exit(0)
		}
	}
}
