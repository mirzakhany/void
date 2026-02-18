package lsp

import (
	"context"
	"path/filepath"
	"sync"
)

// Manager caches LSP clients per (rootURI, languageID) so one server is shared for all files of that language in a project.
type Manager struct {
	config *Config
	mu     sync.Mutex
	byKey  map[string]*Client
}

// NewManager creates a manager that uses the given config to start servers.
func NewManager(config *Config) *Manager {
	return &Manager{
		config: config,
		byKey:  make(map[string]*Client),
	}
}

func (m *Manager) key(rootURI, languageID string) string {
	return rootURI + "\x00" + languageID
}

// ClientFor returns an LSP client for the given file path. It uses projectRoot as workspace root
// and picks the server from config by file extension. Returns nil if no server is configured.
func (m *Manager) ClientFor(ctx context.Context, projectRoot, filePath string) (*Client, error) {
	if m.config == nil {
		return nil, nil
	}
	entry := m.config.ServerForFile(filePath)
	if entry == nil {
		return nil, nil
	}
	rootURI := RootURIFromPath(projectRoot)
	k := m.key(rootURI, entry.LanguageID)

	m.mu.Lock()
	if c, ok := m.byKey[k]; ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	c, err := NewClient(ctx, rootURI, entry.Command, entry.Args)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.byKey[k]; ok {
		m.mu.Unlock()
		_ = c.Close()
		return existing, nil
	}
	m.byKey[k] = c
	m.mu.Unlock()
	return c, nil
}

// RootURIFromPath returns a file URI for the given directory path (workspace root).
func RootURIFromPath(projectRoot string) string {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return string(FileURI(projectRoot))
	}
	return string(FileURI(abs))
}
