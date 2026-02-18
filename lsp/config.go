package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ServerEntry describes one language server: when to run it and how.
type ServerEntry struct {
	// LanguageID is the LSP language identifier (e.g. "go", "python").
	LanguageID string `json:"languageId"`
	// Extensions lists file extensions that use this server (e.g. [".go"], [".py"]).
	Extensions []string `json:"extensions"`
	// Command is the executable name or path (e.g. "gopls", "pylsp").
	Command string `json:"command"`
	// Args are optional arguments passed to the command.
	Args []string `json:"args,omitempty"`
}

// Config holds the LSP server configuration (loadable from JSON without recompiling).
type Config struct {
	Servers []ServerEntry `json:"servers"`
}

// LoadConfig reads LSP config from the first existing path:
// .void/lsp.json (project), then ~/.config/void/lsp.json (user).
// If no file is found, returns DefaultConfig() so common servers (e.g. gopls) work without setup.
func LoadConfig(projectRoot string) *Config {
	paths := []string{
		filepath.Join(projectRoot, ".void", "lsp.json"),
	}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "void", "lsp.json"))
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c Config
		if err := json.Unmarshal(data, &c); err != nil {
			continue
		}
		return &c
	}
	return DefaultConfig()
}

// DefaultConfig returns a minimal config with common language servers.
// Users can override by adding .void/lsp.json or ~/.config/void/lsp.json.
func DefaultConfig() *Config {
	return &Config{
		Servers: []ServerEntry{
			{LanguageID: "go", Extensions: []string{".go"}, Command: "gopls", Args: []string{}},
		},
	}
}

// ServerForFile returns the ServerEntry for the given file path and config.
// It matches by file extension. Returns nil if no server is configured.
func (c *Config) ServerForFile(path string) *ServerEntry {
	if c == nil {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	for i := range c.Servers {
		e := &c.Servers[i]
		for _, eext := range e.Extensions {
			if strings.ToLower(eext) == ext {
				return e
			}
		}
	}
	return nil
}
