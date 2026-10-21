package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// MCPClient represents a known MCP client application.
type MCPClient struct {
	Name       string // Internal name (claude, cursor, vscode, kiro)
	Display    string // Display name for UI
	ConfigPath string // Relative path from home directory
}

// knownClients lists the MCP clients we know how to configure.
var knownClients = []MCPClient{
	{Name: "claude", Display: "Claude Desktop", ConfigPath: filepath.Join("claude", "claude_desktop_config.json")},
	{Name: "cursor", Display: "Cursor", ConfigPath: filepath.Join(".cursor", "mcp.json")},
	{Name: "vscode", Display: "VS Code", ConfigPath: filepath.Join(".vscode", "mcp.json")},
	{Name: "kiro", Display: "Kiro IDE", ConfigPath: filepath.Join(".kiro", "settings.json")},
}

// DetectMCPClient attempts to detect which MCP client is installed by looking
// for config files in the user's home directory. Returns the first found
// client, or empty string if none found.
func DetectMCPClient() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return detectMCPClientInDir(home)
}

// detectMCPClientInDir is the testable version that accepts a base directory.
func detectMCPClientInDir(baseDir string) string {
	for _, client := range knownClients {
		configPath := filepath.Join(baseDir, client.ConfigPath)
		if _, err := os.Stat(configPath); err == nil {
			return client.Name
		}
	}
	return ""
}

// mcpConfigPathForClient returns the full config file path for a given client.
func mcpConfigPathForClient(client, homeDir string) string {
	for _, c := range knownClients {
		if c.Name == client {
			return filepath.Join(homeDir, c.ConfigPath)
		}
	}
	return ""
}

// MCPConfigFile represents the structure of an MCP config file.
type MCPConfigFile struct {
	MCPServers map[string]any `json:"mcpServers"`
}

// parseMCPConfig parses an MCP config file.
func parseMCPConfig(data []byte) (*MCPConfigFile, error) {
	var cfg MCPConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// If mcpServers is present but not an object, that's an error
	// (We check this by trying to marshal and unmarshal as map[string]any)
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]any)
	}

	return &cfg, nil
}

// mergeMCPConfig merges a new server configuration into an existing config file.
// If the server already exists, it is replaced. Other servers are preserved.
func mergeMCPConfig(existingData []byte, serverName string, serverConfig map[string]any) ([]byte, error) {
	// Handle empty or missing file
	if len(existingData) == 0 {
		existingData = []byte("{}")
	}

	// Parse into a generic map to preserve unknown fields
	var raw map[string]any
	if err := json.Unmarshal(existingData, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse existing config: %w", err)
	}

	// Get or create mcpServers
	servers, ok := raw["mcpServers"].(map[string]any)
	if !ok {
		// mcpServers doesn't exist or is wrong type
		servers = make(map[string]any)
		raw["mcpServers"] = servers
	}

	// Add/replace our server
	servers[serverName] = serverConfig

	// Marshal with indentation
	merged, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged config: %w", err)
	}

	return merged, nil
}

// WriteMCPConfig writes an MCP server config to the appropriate file for the
// given client. If the config file exists, the server is merged in; otherwise
// a new file is created.
func WriteMCPConfig(client, serverName string, serverConfig map[string]any) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := mcpConfigPathForClient(client, home)
	if configPath == "" {
		return fmt.Errorf("unknown MCP client: %s", client)
	}

	// Read existing config if present.
	// configPath is resolved from a known client id + the operator's own $HOME;
	// this is a local CLI writing the user's own config.
	var existingData []byte
	// #nosec G304 -- see above: local CLI reading the operator's own config path.
	if data, err := os.ReadFile(configPath); err == nil {
		existingData = data
	}

	// Merge
	merged, err := mergeMCPConfig(existingData, serverName, serverConfig)
	if err != nil {
		return err
	}

	// Ensure directory exists (0700: the merged config can carry tokens).
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write atomically. 0600 because the merged MCP config may contain bearer
	// tokens / secrets — it must not be world- or group-readable.
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, merged, 0o600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	if err := os.Rename(tmpPath, configPath); err != nil {
		_ = os.Remove(tmpPath) // Clean up, ignore error
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// browserOpenerCommand returns the platform-specific command to open a URL.
func browserOpenerCommand() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "linux":
		return "xdg-open"
	case "windows":
		return "cmd"
	default:
		return "xdg-open" // Fallback
	}
}

// OpenBrowser opens the given URL in the default browser.
func OpenBrowser(url string) error {
	cmd := browserOpenerCommand()

	var args []string
	if runtime.GOOS == "windows" {
		args = []string{"/c", "start", url}
	} else {
		args = []string{url}
	}

	// #nosec G204 -- cmd is a fixed platform opener (open/xdg-open/cmd) and the
	// only variable arg is a URL this CLI itself constructed; local operator action.
	return exec.Command(cmd, args...).Start()
}
