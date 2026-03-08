package engine

import (
	"testing"
	"time"
)

func TestParseExtensions(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", []string{".go"}},
		{".go", []string{".go"}},
		{".go,.html", []string{".go", ".html"}},
		{".go, .html, .yaml", []string{".go", ".html", ".yaml"}},
		{"go,html", []string{"go", "html"}},
	}

	for _, tt := range tests {
		got := parseExtensions(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("parseExtensions(%q) = %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parseExtensions(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestCrashLoopDetection(t *testing.T) {
	cfg := Config{
		Root:       ".",
		BuildCmd:   "echo build",
		ExecCmd:    "echo run",
		DebounceMs: 100,
	}

	e := &Engine{
		cfg:            cfg,
		crashThreshold: 3,
		crashWindow:    10 * time.Second,
		crashCooldown:  1 * time.Second,
	}

	// Should not be in crash loop initially
	if e.inCrashLoop() {
		t.Error("should not be in crash loop initially")
	}

	// Record crashes below threshold
	e.recordCrash()
	e.recordCrash()
	if e.inCrashLoop() {
		t.Error("should not be in crash loop with 2 crashes (threshold 3)")
	}

	// Hit threshold
	e.recordCrash()
	if !e.inCrashLoop() {
		t.Error("should be in crash loop after 3 crashes")
	}
}
