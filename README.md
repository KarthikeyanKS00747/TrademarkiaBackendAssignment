# hotreload

A CLI tool that watches a Go project folder for code changes and automatically rebuilds and restarts the server.

## Features

### Core
- **File watching** — Monitors directories recursively using `fsnotify`
- **Auto rebuild & restart** — Rebuilds and restarts the server on any code change
- **Immediate first build** — Triggers build+start when the tool launches (no waiting for a file change)
- **Real-time log streaming** — Server stdout/stderr streamed without buffering
- **Fast response** — File change to server restart in under ~2 seconds

### Debouncing & Cancellation
- **Event debouncing** — Coalesces rapid file events (e.g., editor save) to avoid redundant rebuilds
- **Build cancellation** — If a new change arrives during a build, the current build is cancelled and only the latest state is built

### Process Management
- **Process tree killing** — Kills the server and all its child processes (uses `taskkill /T` on Windows, process group `SIGTERM`/`SIGKILL` on Unix)
- **Stubborn process handling** — Falls back to force-kill if a process doesn't terminate within 5 seconds
- **Resource cleanup** — Ensures processes are fully terminated before starting new ones

### Stability
- **Crash loop detection** — If the server crashes 5+ times within 10 seconds, a cooldown delay is applied to prevent rapid restart loops
- **Graceful error handling** — Build errors and server crash are logged without crashing the tool

### File Filtering
Automatically ignores:
- `.git/`, `node_modules/`, `vendor/`, `build/`, `dist/`, `bin/`, `tmp/`
- Editor temp files (`*.swp`, `*~`, `*.tmp`, `*.bak`)
- Hidden files (dot-prefixed)

### Scalability
- **Dynamic directory watching** — Detects and watches newly created folders; handles deleted folders gracefully
- **Configurable extensions** — Watch only specific file types (default: `.go`)

## Installation

```bash
go install github.com/hotreload/cmd/hotreload@latest
```

Or build from source:

```bash
git clone <repo-url>
cd hotreload
go build -o bin/hotreload ./cmd/hotreload
```

## Usage

```bash
hotreload --root <project-folder> --build "<build-command>" --exec "<run-command>"
```

### Parameters

| Flag | Description | Default |
|------|-------------|---------|
| `--root` | Directory to watch for file changes | `.` |
| `--build` | Build command to run on changes | *required* |
| `--exec` | Command to start the server | *required* |
| `--debounce` | Debounce interval in milliseconds | `300` |
| `--ext` | Comma-separated file extensions to watch | `.go` |
| `--version` | Print version and exit | |

### Example

```bash
hotreload \
  --root ./myproject \
  --build "go build -o ./bin/server ./cmd/server" \
  --exec "./bin/server"
```

## Demo

A sample test server is included in `testserver/`.

### Linux/macOS

```bash
make demo
```

### Windows

```powershell
# Build hotreload
go build -o bin/hotreload.exe ./cmd/hotreload

# Run the demo
.\bin\hotreload.exe --root .\testserver --build "go build -o ./bin/testserver.exe ./testserver" --exec "./bin/testserver.exe"
```

Then edit `testserver/main.go` (e.g., change the `"message"` string) and save — the server restarts automatically.

## Architecture

```
cmd/hotreload/          CLI entry point (flag parsing, signal handling)
internal/
  engine/               Orchestrator: ties watcher + process manager together
    engine.go           Main run loop with debouncing, crash loop detection
  watcher/              File system watcher
    watcher.go          fsnotify wrapper with filtering, dynamic dir watching
  process/              Process lifecycle manager
    process.go          Build execution, server start/stop, process tree killing
    proc_unix.go        Unix-specific process group setup
    proc_windows.go     Windows-specific process creation flags
testserver/             Sample HTTP server for demo
```

### Flow

1. **Startup** → Parse flags → Initialize watcher + process manager → Trigger first build+start
2. **Watch loop** → fsnotify events → Filter (extension, ignore patterns) → Debounce (300ms) → Cancel any in-progress build → Build → Stop old server → Start new server
3. **Shutdown** → OS signal (Ctrl+C) → Stop server → Close watcher → Exit

## Running Tests

```bash
go test -v -race ./...
```

## Design Decisions

1. **fsnotify for file events** — Allowed by the assignment, provides cross-platform OS-level file watching.
2. **Process groups for cleanup** — Using `Setpgid` (Unix) and `CREATE_NEW_PROCESS_GROUP` (Windows) ensures child processes are killed when the server restarts.
3. **Context-based build cancellation** — Go's `context.Context` is used to cancel builds mid-flight when new changes arrive.
4. **Crash loop protection** — A sliding window counter prevents the tool from endlessly restarting a broken server.
5. **Channel-based event pipeline** — Decouples event detection from processing, with debouncing in the engine's main select loop.
6. **log/slog** — Uses the standard library structured logger as required.

## License

MIT
