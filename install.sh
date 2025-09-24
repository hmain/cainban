#!/bin/bash

# Cainban Installation Script
# First tries to build from source, then downloads release if source unavailable

set -euo pipefail  # Exit on error, undefined vars, pipe failures

# Global variables
readonly LOCAL_BIN="$HOME/.local/bin"
readonly BINARY_NAME="cainban"
readonly REPO_URL="https://github.com/hmain/cainban"

# Command line options
DRY_RUN=false

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                show_help
                exit 1
                ;;
        esac
    done
}

# Show help message
show_help() {
    cat << EOF
Cainban Installation Script

USAGE:
    $0 [OPTIONS]

OPTIONS:
    --dry-run    Test installation without actually installing
    -h, --help   Show this help message

EXAMPLES:
    $0                # Install cainban
    $0 --dry-run      # Test installation scenarios
EOF
}

# Logging functions
log_info() {
    echo "🔧 $*" >&2
}

log_success() {
    echo "✅ $*" >&2
}

log_warning() {
    echo "⚠️  $*" >&2
}

log_error() {
    echo "❌ $*" >&2
}

log_dry_run() {
    echo "🧪 [DRY RUN] $*" >&2
}

# Dry-run wrapper for commands
dry_run_or_execute() {
    local description="$1"
    shift
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Would execute: $*"
        log_dry_run "$description"
        return 0
    else
        "$@"
    fi
}

# Dry-run wrapper for file operations
dry_run_file_op() {
    local description="$1"
    local operation="$2"
    shift 2
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "$description"
        return 0
    else
        case "$operation" in
            mkdir) mkdir "$@" ;;
            curl) curl "$@" ;;
            mv) mv "$@" ;;
            chmod) chmod "$@" ;;
            rm) rm "$@" ;;
            *) log_error "Unknown file operation: $operation"; return 1 ;;
        esac
    fi
}

# Error handler
cleanup_on_error() {
    local exit_code=$?
    if [[ "$DRY_RUN" == "true" ]]; then
        log_error "Dry run failed with exit code $exit_code"
    else
        log_error "Installation failed with exit code $exit_code"
        # Clean up partial downloads
        if [[ -f "$LOCAL_BIN/$BINARY_NAME.tmp" ]]; then
            rm -f "$LOCAL_BIN/$BINARY_NAME.tmp"
        fi
    fi
    exit "$exit_code"
}

# Set up error handling
trap cleanup_on_error ERR

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if curl is available for downloads
    if ! command -v curl >/dev/null 2>&1; then
        log_error "curl is required but not installed"
        exit 1
    fi
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "✓ curl is available"
    fi
}

# Ensure local bin directory exists and is in PATH
setup_local_bin() {
    log_info "Setting up local bin directory..."
    
    dry_run_file_op "Would create directory: $LOCAL_BIN" mkdir -p "$LOCAL_BIN"
    
    # Check if ~/.local/bin is in PATH
    if [[ ":$PATH:" != *":$LOCAL_BIN:"* ]]; then
        log_warning "$LOCAL_BIN is not in your PATH"
        log_warning "Add this to your shell profile (.bashrc, .zshrc, etc.):"
        log_warning "export PATH=\"\$HOME/.local/bin:\$PATH\""
        echo
    fi
}

# Check if source build is possible
can_build_from_source() {
    [[ -f "go.mod" ]] && [[ -f "cmd/cainban/main.go" ]] && command -v go >/dev/null 2>&1
}

# Build from source
build_from_source() {
    log_info "Building from source..."
    
    # Validate source files exist
    if [[ ! -f "go.mod" ]]; then
        log_error "go.mod not found in current directory"
        return 1
    fi
    
    if [[ ! -f "cmd/cainban/main.go" ]]; then
        log_error "cmd/cainban/main.go not found"
        return 1
    fi
    
    # Check Go installation
    if ! command -v go >/dev/null 2>&1; then
        log_error "Go is not installed or not in PATH"
        return 1
    fi
    
    # Build with proper error handling
    export GOBIN="$LOCAL_BIN"
    dry_run_or_execute "Built from source successfully" \
        go build -o "$LOCAL_BIN/$BINARY_NAME" ./cmd/cainban
    
    if [[ "$DRY_RUN" == "false" ]]; then
        log_success "Built from source successfully"
    fi
    return 0
}

# Detect platform for release download
detect_platform() {
    local os arch platform
    
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    
    case "$arch" in
        x86_64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) 
            log_error "Unsupported architecture: $arch"
            exit 1
            ;;
    esac
    
    case "$os" in
        linux) platform="linux-$arch" ;;
        darwin) platform="darwin-$arch" ;;
        *) 
            log_error "Unsupported OS: $os"
            exit 1
            ;;
    esac
    
    echo "$platform"
}

# Check if release download is possible
can_download_release() {
    local platform binary_name download_url
    platform=$(detect_platform)
    binary_name="cainban-$platform"
    download_url="$REPO_URL/releases/latest/download/$binary_name"
    
    # For dry-run, just check if curl works and platform is supported
    if [[ "$DRY_RUN" == "true" ]]; then
        command -v curl >/dev/null 2>&1
    else
        # For real execution, test URL accessibility
        curl -fsSL --head "$download_url" >/dev/null 2>&1
    fi
}

# Download release binary
download_release() {
    log_info "Downloading release..."
    
    local platform binary_name download_url temp_file
    platform=$(detect_platform)
    binary_name="cainban-$platform"
    download_url="$REPO_URL/releases/latest/download/$binary_name"
    temp_file="$LOCAL_BIN/$BINARY_NAME.tmp"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Platform detected: $platform"
        log_dry_run "Would download from: $download_url"
        log_dry_run "Would save to: $temp_file"
        log_dry_run "Would move to: $LOCAL_BIN/$BINARY_NAME"
        log_dry_run "Would make executable"
        return 0
    fi
    
    log_info "Downloading from: $download_url"
    
    # Download to temporary file first
    if ! curl -fsSL -o "$temp_file" "$download_url"; then
        log_error "Failed to download release from $download_url"
        log_error "Please check your internet connection and try again"
        return 1
    fi
    
    # Verify download succeeded and file is not empty
    if [[ ! -s "$temp_file" ]]; then
        log_error "Downloaded file is empty or corrupted"
        rm -f "$temp_file"
        return 1
    fi
    
    # Move to final location and make executable
    if ! mv "$temp_file" "$LOCAL_BIN/$BINARY_NAME"; then
        log_error "Failed to move binary to $LOCAL_BIN/$BINARY_NAME"
        rm -f "$temp_file"
        return 1
    fi
    
    if ! chmod +x "$LOCAL_BIN/$BINARY_NAME"; then
        log_error "Failed to make binary executable"
        return 1
    fi
    
    log_success "Downloaded release successfully"
    return 0
}

# Verify installation
verify_installation() {
    log_info "Verifying installation..."
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Would check: command -v $BINARY_NAME"
        log_dry_run "Would test: $BINARY_NAME version"
        return 0
    fi
    
    if ! command -v "$BINARY_NAME" >/dev/null 2>&1; then
        log_error "Installation verification failed"
        log_error "Make sure $LOCAL_BIN is in your PATH"
        return 1
    fi
    
    # Test that the binary actually works
    if ! "$BINARY_NAME" version >/dev/null 2>&1; then
        log_error "Binary installed but not working correctly"
        return 1
    fi
    
    log_success "Installation verified successfully"
    return 0
}

# Dry-run analysis
analyze_installation_options() {
    log_dry_run "Analyzing installation options..."
    
    local source_possible=false
    local release_possible=false
    
    if can_build_from_source; then
        log_dry_run "✓ Source build is possible (go.mod, main.go, and Go found)"
        source_possible=true
    else
        log_dry_run "✗ Source build not possible"
        if [[ ! -f "go.mod" ]]; then
            log_dry_run "  - go.mod not found"
        fi
        if [[ ! -f "cmd/cainban/main.go" ]]; then
            log_dry_run "  - cmd/cainban/main.go not found"
        fi
        if ! command -v go >/dev/null 2>&1; then
            log_dry_run "  - Go not installed"
        fi
    fi
    
    if can_download_release; then
        log_dry_run "✓ Release download is possible"
        release_possible=true
    else
        log_dry_run "✗ Release download not possible"
    fi
    
    if [[ "$source_possible" == "true" ]]; then
        log_dry_run "→ Would install from source"
        return 0
    elif [[ "$release_possible" == "true" ]]; then
        log_dry_run "→ Would install from release"
        return 0
    else
        log_dry_run "→ Installation would fail (no viable options)"
        return 1
    fi
}

# Main installation logic
main() {
    parse_args "$@"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        log_dry_run "Starting dry run - no files will be modified"
        echo
    else
        log_info "Installing Cainban..."
    fi
    
    check_prerequisites
    setup_local_bin
    
    # For dry-run, analyze options first
    if [[ "$DRY_RUN" == "true" ]]; then
        if ! analyze_installation_options; then
            exit 1
        fi
    fi
    
    # Try building from source first, fallback to release download
    if build_from_source; then
        if [[ "$DRY_RUN" == "true" ]]; then
            log_dry_run "✓ Would install from source"
        else
            log_success "Installation complete (built from source)"
        fi
    elif download_release; then
        if [[ "$DRY_RUN" == "true" ]]; then
            log_dry_run "✓ Would install from release"
        else
            log_success "Installation complete (downloaded release)"
        fi
    else
        if [[ "$DRY_RUN" == "true" ]]; then
            log_dry_run "✗ Installation would fail"
        else
            log_error "Both source build and release download failed"
        fi
        exit 1
    fi
    
    # Verify the installation works
    if ! verify_installation; then
        exit 1
    fi
    
    if [[ "$DRY_RUN" == "true" ]]; then
        echo
        log_dry_run "Dry run completed successfully"
        log_dry_run "Run without --dry-run to perform actual installation"
    else
        echo
        log_info "Verifying installation:"
        "$BINARY_NAME" version
        echo
        log_success "Ready to use! Try: $BINARY_NAME tui"
    fi
}

# Run main function
main "$@"