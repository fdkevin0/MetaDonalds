# List available tasks.
default:
    @just --list

# Build the server, browser launcher, and terminal executable.
build: frontend
    go build -o bin/metadonalds ./cmd/metadonalds

# Install frontend dependencies, check types, and build embedded assets.
frontend:
    bun install --cwd frontend --frozen-lockfile
    bun run --cwd frontend build

# Build frontend assets and run frontend tests and Go race tests.
test: frontend
    bun run --cwd frontend test
    go test -race ./...
