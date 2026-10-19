package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDetectMCPClient verifies MCP client detection from config file presence.
func TestDetectMCPClient(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	tests := []struct {
		name       string
		setupFiles []string
		wantClient string
	}{
		{
			name:       "no config files",
			setupFiles: nil,
			wantClient: "",
		},
		{
			name:       "claude code config",
			setupFiles: []string{filepath.Join("claude", "claude_desktop_config.json")},
			wantClient: "claude",
		},
		{
			name:       "cursor config",
			setupFiles: []string{filepath.Join(".cursor", "mcp.json")},
			wantClient: "cursor",
		},
		{
			name:       "vscode config",
			setupFiles: []string{filepath.Join(".vscode", "mcp.json")},
			wantClient: "vscode",
		},
		{
			name:       "kiro config",
			setupFiles: []string{filepath.Join(".kiro", "settings.json")},
			wantClient: "kiro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh temp dir for each test
			testDir := filepath.Join(tmpDir, tt.name)
			os.MkdirAll(testDir, 0755)

			// Create config files
			for _, f := range tt.setupFiles {
				fpath := filepath.Join(testDir, f)
				os.MkdirAll(filepath.Dir(fpath), 0755)
				os.WriteFile(fpath, []byte("{}"), 0644)
			}

			// Detect client
			client := detectMCPClientInDir(testDir)
			if client != tt.wantClient {
				t.Errorf("detectMCPClientInDir() = %q, want %q", client, tt.wantClient)
			}
		})
	}
}

// TestMCPConfigPath returns the expected config file path for each client.
func TestMCPConfigPath(t *testing.T) {
	home := "/home/testuser"
	if runtime.GOOS == "windows" {
		home = `C:\Users\testuser`
	}

	tests := []struct {
		client   string
		wantPath string
	}{
		{
			client:   "claude",
			wantPath: filepath.Join(home, "claude", "claude_desktop_config.json"),
		},
		{
			client:   "cursor",
			wantPath: filepath.Join(home, ".cursor", "mcp.json"),
		},
		{
			client:   "vscode",
			wantPath: filepath.Join(home, ".vscode", "mcp.json"),
		},
		{
			client:   "kiro",
			wantPath: filepath.Join(home, ".kiro", "settings.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.client, func(t *testing.T) {
			got := mcpConfigPathForClient(tt.client, home)
			if got != tt.wantPath {
				t.Errorf("mcpConfigPathForClient(%q) = %q, want %q", tt.client, got, tt.wantPath)
			}
		})
	}
}

// TestMCPConfigMerge verifies merging cainban server into existing config.
func TestMCPConfigMerge(t *testing.T) {
	tests := []struct {
		name         string
		existingJSON string
		serverName   string
		serverConfig map[string]any
		wantServers  int
	}{
		{
			name:         "empty config",
			existingJSON: `{}`,
			serverName:   "cainban",
			serverConfig: map[string]any{"url": "https://mcp.example.com"},
			wantServers:  1,
		},
		{
			name:         "existing servers",
			existingJSON: `{"mcpServers":{"other":{"url":"https://other.com"}}}`,
			serverName:   "cainban",
			serverConfig: map[string]any{"url": "https://mcp.example.com"},
			wantServers:  2,
		},
		{
			name:         "replace existing cainban",
			existingJSON: `{"mcpServers":{"cainban":{"url":"https://old.com"},"other":{"url":"https://other.com"}}}`,
			serverName:   "cainban",
			serverConfig: map[string]any{"url": "https://mcp.example.com"},
			wantServers:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := mergeMCPConfig([]byte(tt.existingJSON), tt.serverName, tt.serverConfig)
			if err != nil {
				t.Fatalf("mergeMCPConfig() error = %v", err)
			}

			// Parse and verify
			parsed, err := parseMCPConfig(merged)
			if err != nil {
				t.Fatalf("failed to parse merged config: %v", err)
			}

			if len(parsed.MCPServers) != tt.wantServers {
				t.Errorf("got %d servers, want %d", len(parsed.MCPServers), tt.wantServers)
			}

			if _, ok := parsed.MCPServers[tt.serverName]; !ok {
				t.Errorf("server %q not found in merged config", tt.serverName)
			}
		})
	}
}

// TestParseExistingMCPConfig verifies parsing various MCP config formats.
func TestParseExistingMCPConfig(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantOK  bool
		servers int
	}{
		{
			name:    "empty object",
			json:    `{}`,
			wantOK:  true,
			servers: 0,
		},
		{
			name:    "with servers",
			json:    `{"mcpServers":{"server1":{},"server2":{}}}`,
			wantOK:  true,
			servers: 2,
		},
		{
			name:   "invalid json",
			json:   `{not valid}`,
			wantOK: false,
		},
		{
			name:    "servers as array (invalid)",
			json:    `{"mcpServers":[]}`,
			wantOK:  false,
			servers: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parseMCPConfig([]byte(tt.json))
			if tt.wantOK {
				if err != nil {
					t.Errorf("parseMCPConfig() error = %v, wantOK = true", err)
				} else if len(parsed.MCPServers) != tt.servers {
					t.Errorf("got %d servers, want %d", len(parsed.MCPServers), tt.servers)
				}
			} else if err == nil {
				t.Error("parseMCPConfig() expected error, got nil")
			}
		})
	}
}

// TestBrowserOpenerCommand verifies platform-specific browser commands.
func TestBrowserOpenerCommand(t *testing.T) {
	cmd := browserOpenerCommand()

	// Should return a valid command for the current platform
	switch runtime.GOOS {
	case "darwin":
		if cmd != "open" {
			t.Errorf("on darwin, expected 'open', got %q", cmd)
		}
	case "linux":
		if cmd != "xdg-open" {
			t.Errorf("on linux, expected 'xdg-open', got %q", cmd)
		}
	case "windows":
		if cmd != "cmd" {
			t.Errorf("on windows, expected 'cmd', got %q", cmd)
		}
	default:
		// Unknown platform - just make sure it doesn't panic
		t.Logf("unknown platform %s, got command %q", runtime.GOOS, cmd)
	}
}
