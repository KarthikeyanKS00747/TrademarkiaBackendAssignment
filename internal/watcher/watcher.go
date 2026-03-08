package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

// IgnorePatterns defines directories and file patterns to ignore.
var DefaultIgnorePatterns = []string{
	".git",
	"node_modules",
	"vendor",
	".idea",
	".vscode",
	"__pycache__",
	".DS_Store",
	"bin",
	"dist",
	"build",
	"tmp",
}

// IgnoreSuffixes defines file suffixes to ignore (temp/editor files).
var DefaultIgnoreSuffixes = []string{
	"~",
	".swp",
	".swo",
	".swx",
	".tmp",
	".bak",
	"#",
}

// IgnorePrefixes defines file prefixes to ignore.
var DefaultIgnorePrefixes = []string{
	".",
	"#",
}

// Watcher watches a directory tree for file changes.
type Watcher struct {
	fsWatcher  *fsnotify.Watcher
	root       string
	extensions map[string]bool
	logger     *slog.Logger
	Events     chan string // channel of changed file paths
	mu         sync.Mutex
	watched    map[string]bool
}

// New creates a new Watcher for the given root directory.
func New(root string, extensions []string, logger *slog.Logger) (*Watcher, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	fsW, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	extMap := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		ext = strings.TrimSpace(ext)
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		extMap[ext] = true
	}

	w := &Watcher{
		fsWatcher:  fsW,
		root:       absRoot,
		extensions: extMap,
		logger:     logger,
		Events:     make(chan string, 100),
		watched:    make(map[string]bool),
	}

	return w, nil
}

// Start begins watching. It walks the root directory tree, adds all matching
// directories, and starts processing events. Call this in a goroutine.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.walkAndWatch(w.root); err != nil {
		return err
	}

	w.logger.Info("watching for changes", "root", w.root, "extensions", w.extensionList())

	go w.loop(ctx)
	return nil
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	return w.fsWatcher.Close()
}

func (w *Watcher) loop(ctx context.Context) {
	defer close(w.Events)

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("watcher error", "error", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := event.Name

	// Handle new directories - watch them too
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			if !w.shouldIgnoreDir(filepath.Base(path)) {
				w.logger.Debug("new directory detected, adding watch", "path", path)
				_ = w.walkAndWatch(path)
			}
			return
		}
	}

	// Handle removed directories
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		w.mu.Lock()
		if w.watched[path] {
			delete(w.watched, path)
			// fsnotify automatically removes watches for deleted dirs
			w.logger.Debug("directory removed from watch", "path", path)
		}
		w.mu.Unlock()
	}

	// Filter by operation type - we care about writes, creates, removes, renames
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
		!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return
	}

	// Filter by file name
	if w.shouldIgnoreFile(path) {
		return
	}

	// Filter by extension
	ext := filepath.Ext(path)
	if len(w.extensions) > 0 && !w.extensions[ext] {
		return
	}

	w.logger.Debug("file change detected", "path", path, "op", event.Op.String())

	// Non-blocking send
	select {
	case w.Events <- path:
	default:
		// Channel full, drain and resend to keep latest
		select {
		case <-w.Events:
		default:
		}
		w.Events <- path
	}
}

func (w *Watcher) walkAndWatch(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// If a directory can't be accessed, skip it
			if info != nil && info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !info.IsDir() {
			return nil
		}

		name := filepath.Base(path)
		if w.shouldIgnoreDir(name) {
			return filepath.SkipDir
		}

		w.mu.Lock()
		defer w.mu.Unlock()

		if w.watched[path] {
			return nil
		}

		if err := w.fsWatcher.Add(path); err != nil {
			// Detect OS-level watch limit errors and provide actionable guidance.
			if isWatchLimitError(err) {
				w.logger.Error("OS watch limit reached — cannot watch more directories",
					"path", path,
					"error", err,
					"hint_linux", "increase limit: echo 524288 | sudo tee /proc/sys/fs/inotify/max_user_watches",
					"hint_macos", "increase limit: sudo sysctl -w kern.maxfiles=524288",
				)
				return fmt.Errorf("watch limit reached: %w", err)
			}
			w.logger.Warn("failed to watch directory", "path", path, "error", err)
			return nil // Don't stop walking on other watch errors
		}

		w.watched[path] = true
		w.logger.Debug("watching directory", "path", path)
		return nil
	})
}

func (w *Watcher) shouldIgnoreDir(name string) bool {
	for _, pattern := range DefaultIgnorePatterns {
		if strings.EqualFold(name, pattern) {
			return true
		}
	}
	return false
}

func (w *Watcher) shouldIgnoreFile(path string) bool {
	name := filepath.Base(path)

	for _, suffix := range DefaultIgnoreSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	for _, prefix := range DefaultIgnorePrefixes {
		if strings.HasPrefix(name, prefix) && name != "." && name != ".." {
			return true
		}
	}

	return false
}

func (w *Watcher) extensionList() []string {
	exts := make([]string, 0, len(w.extensions))
	for ext := range w.extensions {
		exts = append(exts, ext)
	}
	return exts
}

// WatchedDirCount returns the number of directories currently watched.
func (w *Watcher) WatchedDirCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watched)
}

// EventsChan returns the channel of changed file paths (implements FileWatcher).
func (w *Watcher) EventsChan() <-chan string {
	return w.Events
}

// isWatchLimitError returns true if the error indicates the OS inotify/kqueue
// watch limit has been reached.
func isWatchLimitError(err error) bool {
	// Linux: ENOSPC means inotify watch limit exceeded
	// macOS: EMFILE means too many open file descriptors
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ENOSPC || errno == syscall.EMFILE
	}
	// Fallback: check the error message for common patterns
	msg := err.Error()
	return strings.Contains(msg, "too many open files") ||
		strings.Contains(msg, "no space left on device") ||
		strings.Contains(msg, "inotify_add_watch")
}
