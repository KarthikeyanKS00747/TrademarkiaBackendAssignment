.PHONY: build run demo test clean install

# Build the hotreload binary
build:
	go build -o bin/hotreload ./cmd/hotreload

# Build the test server
build-testserver:
	go build -o bin/testserver ./testserver

# Run the demo: hotreload watching the testserver
demo: build
	./bin/hotreload \
		--root ./testserver \
		--build "go build -o ./bin/testserver ./testserver" \
		--exec "./bin/testserver"

# Run on Windows (use this on Windows)
demo-win: build
	.\bin\hotreload.exe \
		--root .\testserver \
		--build "go build -o ./bin/testserver.exe ./testserver" \
		--exec "./bin/testserver.exe"

# Install hotreload to $GOPATH/bin
install:
	go install ./cmd/hotreload

# Run tests
test:
	go test -v -race -count=1 ./...

# Run tests with coverage
test-cover:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Lint (requires golangci-lint)
lint:
	golangci-lint run ./...
