#!/bin/bash
# Build and run the hotreload demo on Linux/macOS
set -e

echo "Building hotreload..."
go build -o bin/hotreload ./cmd/hotreload

echo "Starting hotreload with testserver..."
./bin/hotreload --root ./testserver --build "go build -o ./bin/testserver ./testserver" --exec "./bin/testserver"
