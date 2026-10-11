# Match Go 1.27's minimum macOS version for both native compilation and linking.
export MACOSX_DEPLOYMENT_TARGET := if os() == "macos" { "13.0" } else { env("MACOSX_DEPLOYMENT_TARGET", "") }
export CGO_CFLAGS := env("CGO_CFLAGS", "-O2 -g") + if os() == "macos" { " -mmacosx-version-min=13.0" } else { "" }
export CGO_LDFLAGS := env("CGO_LDFLAGS", "-O2 -g") + if os() == "macos" { " -mmacosx-version-min=13.0" } else { "" }

# List available tasks.
default:
    @just --list

# Build the server and terminal executable.
build: frontend
    go build -o bin/metadonalds ./cmd/metadonalds

# Build the executable with Wails desktop support.
desktop: frontend
    go build -tags desktop -o bin/desktop/metadonalds ./cmd/metadonalds

# Install frontend dependencies, check types, and build embedded assets.
frontend:
    bun install --cwd frontend --frozen-lockfile
    bun run --cwd frontend build

# Build frontend assets and run frontend tests and Go race tests.
test: frontend
    bun run --cwd frontend test
    go test -race ./...

# Test the desktop build with the same native compiler settings as desktop.
test-desktop: frontend
    go test -tags desktop ./...
