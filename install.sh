#!/bin/bash

# Cainban Installation Script
# Ensures proper installation to user's local bin directory

set -e  # Exit on any error

echo "🔧 Installing Cainban..."

# Ensure ~/.local/bin exists and is in PATH
mkdir -p ~/.local/bin

# Check if ~/.local/bin is in PATH
if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Warning: ~/.local/bin is not in your PATH"
    echo "   Add this to your shell profile (.bashrc, .zshrc, etc.):"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo
fi

# Set GOBIN to ensure installation to correct location
export GOBIN="$HOME/.local/bin"

# Build and install
echo "🏗️  Building cainban..."
go build -o "$GOBIN/cainban" ./cmd/cainban

# Verify installation
echo "✅ Installation complete!"
echo
echo "🔍 Verifying installation:"
if command -v cainban >/dev/null 2>&1; then
    cainban version
    echo
    echo "🚀 Ready to use! Try: cainban tui"
else
    echo "❌ Installation verification failed"
    echo "   Make sure ~/.local/bin is in your PATH"
    exit 1
fi