package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"github.com/chapar-rest/uikit/tabs"
	"github.com/chapar-rest/uikit/theme"
	"github.com/chapar-rest/uikit/treeview"
	"go.lsp.dev/protocol"
)

var fileTreeIgnoreList = []string{".git", ".idea", ".vscode", ".DS_Store", ".env"}

// buildFileTree creates the file tree from the current directory.
func (s *appState) buildFileTree(th *theme.Theme) *treeview.Tree {
	tree := treeview.NewTree()

	entries, err := os.ReadDir(".")
	if err != nil {
		fmt.Printf("Error reading directory: %v\n", err)
		return tree
	}

	for _, entry := range entries {
		if slices.Contains(fileTreeIgnoreList, entry.Name()) {
			continue
		}
		node := s.buildFileNode(th, entry, ".")
		if node != nil {
			tree.Insert(node)
		}
	}

	return tree
}

// buildFileNode recursively builds a tree node for a file or directory.
func (s *appState) buildFileNode(th *theme.Theme, entry os.DirEntry, parentPath string) *treeview.Node {
	name := entry.Name()
	fullPath := filepath.Join(parentPath, name)

	node := treeview.NewNode(fullPath, func(gtx layout.Context, th *theme.Theme) layout.Dimensions {
		_, _, txt := th.FgBgTxt(theme.KindPrimary, treeview.TreeComponent)
		lb := material.Label(th.Material(), unit.Sp(14), name)
		lb.Color = txt
		return lb.Layout(gtx)
	})

	node.OnClickFunc = func(node *treeview.Node) {
		s.onFileNodeClick(node)
	}

	if entry.IsDir() {
		dirEntries, err := os.ReadDir(fullPath)
		if err == nil {
			for _, childEntry := range dirEntries {
				childNode := s.buildFileNode(th, childEntry, fullPath)
				if childNode != nil {
					node.AddChild(childNode)
				}
			}
		}
	}

	return node
}

// onFileNodeClick handles file/directory clicks in the tree (opens files as tabs).
func (s *appState) onFileNodeClick(node *treeview.Node) {
	s.openFileAsTab(node.ID)
}

// openFileAsTab opens a file at path in a new tab, or selects the tab if already open.
func (s *appState) openFileAsTab(path string) {
	if _, ok := s.openFiles[path]; ok {
		if tab := s.openTabs[path]; tab != nil {
			s.tabitems.SelectTab(tab)
		}
		return
	}

	s.openFiles[path] = s.buildFileView(s.theme, path)

	_, _, txt := s.theme.FgBgTxt(theme.KindPrimary, treeview.TreeComponent)
	t := tabs.NewTab(func(gtx layout.Context, th *theme.Theme) layout.Dimensions {
		lb := material.Label(th.Material(), unit.Sp(14), s.openFiles[path].Title)
		lb.Color = txt
		return lb.Layout(gtx)
	})

	t.OnCloseFunc = func(tab *tabs.Tab) bool {
		p := s.tabToPath[tab]
		if fv, ok := s.openFiles[p]; ok && fv.LSPClient != nil {
			_ = fv.LSPClient.DidClose(context.Background(), protocol.DocumentURI(fv.LSPDocURI))
			fv.LSPClient.UnregisterDiagnosticsHandler(fv.LSPDocURI)
		}
		delete(s.openFiles, p)
		delete(s.openTabs, p)
		delete(s.tabToPath, tab)
		if i := slices.Index(s.openPaths, p); i >= 0 {
			s.openPaths = slices.Delete(s.openPaths, i, i+1)
		}
		return true
	}

	t.State = tabs.TabStateClean
	s.tabitems.AddTab(t)
	s.openTabs[path] = t
	s.openPaths = append(s.openPaths, path)
	s.tabToPath[t] = path
}

// nextUntitledPath returns a unique path for a new file (e.g. "untitled-1", "untitled-2").
func (s *appState) nextUntitledPath() string {
	for i := 1; ; i++ {
		path := fmt.Sprintf("untitled-%d", i)
		if _, ok := s.openFiles[path]; !ok {
			return path
		}
	}
}

// openNewFile creates a new untitled buffer and opens it in a tab.
func (s *appState) openNewFile() {
	s.openFileAsTab(s.nextUntitledPath())
}
