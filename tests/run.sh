#!/usr/bin/env bash
# Orchestrator for Hurl integration tests.
# Builds the server, starts it with isolated data directory,
# runs all .hurl test files, and cleans up on exit.
set -euo pipefail

cd "$(dirname "$0")/.."

# Ensure .env exists (copy from example if needed)
if [[ ! -f .env ]]; then
  echo "==> Creating .env from .env.example"
  cp .env.example .env
fi

# Build the server binary
echo "==> Building server..."
go build -o /tmp/summarize-server ./cmd/server
echo "==> Build OK"

# Create isolated temp data directory
DATA_DIR=$(mktemp -d)
cleanup() {
  local exit_code=$?
  echo "==> Cleaning up..."
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR"
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

# Start the server
echo "==> Starting server (DATA_DIR=$DATA_DIR)..."
export DATA_DIR
/tmp/summarize-server &
SERVER_PID=$!

# Wait for server to become healthy (up to 15 seconds)
echo "==> Waiting for server to be healthy..."
for i in $(seq 1 15); do
  if curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/healthz 2>/dev/null | grep -q 200; then
    echo "==> Server is healthy (attempt $i)"
    break
  fi
  if [[ "$i" -eq 15 ]]; then
    echo "==> ERROR: Server did not become healthy within 15 seconds"
    exit 1
  fi
  sleep 1
done

# Run all Hurl tests
echo "==> Running Hurl integration tests..."
# --retry 60: retry failed requests up to 60 times (for async polling).
# YouTube transcripts + pi summarization can take 30-50 seconds.
# --retry-interval 1000: wait 1s between retries.
hurl --test \
  --retry 60 \
  --retry-interval 1000 \
  --variable HOST=http://localhost:8080 \
  tests/*.hurl
echo "==> All tests completed"
