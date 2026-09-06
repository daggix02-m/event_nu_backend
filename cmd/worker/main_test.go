package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

func TestNewLoggerDevelopmentIsTextDebug(t *testing.T) {
	out := captureStdout(t, func() {
		logger := newLogger("development")
		logger.Debug("marker")
	})

	if !strings.Contains(out, "level=DEBUG") {
		t.Fatalf("expected text handler emitting DEBUG, got: %q", out)
	}
	if !strings.Contains(out, "msg=marker") {
		t.Fatalf("expected marker message, got: %q", out)
	}
}

func TestNewLoggerProductionIsJSONInfo(t *testing.T) {
	out := captureStdout(t, func() {
		logger := newLogger("production")
		logger.Info("marker")
	})

	var rec map[string]any
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("expected a JSON log line, got: %q", out)
	}
	if rec["msg"] != "marker" || rec["level"] != "INFO" {
		t.Fatalf("unexpected log record: %v", rec)
	}
}

func TestNewLoggerProductionSuppressesDebug(t *testing.T) {
	out := captureStdout(t, func() {
		logger := newLogger("production")
		logger.Debug("hidden")
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output at debug level in production, got: %q", out)
	}
}
