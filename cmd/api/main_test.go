package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
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

func TestNewLoggerDevelopmentDropsInfoByDefault(t *testing.T) {
	out := captureStdout(t, func() {
		logger := newLogger("development")
		logger.Info("should-be-visible")
	})
	// Debug level is the dev floor, so this is emitted too.
	if !strings.Contains(out, "msg=should-be-visible") {
		t.Fatalf("expected info line at debug level, got: %q", out)
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

func TestGRPCBindIsWildcard(t *testing.T) {
	cases := map[string]bool{
		":50051":          true,  // bare host:port binds all interfaces
		"0.0.0.0:50051":   true,  // explicit IPv4 wildcard
		"[::]:50051":      true,  // explicit IPv6 wildcard
		"127.0.0.1:50051": false, // loopback
		"10.0.0.5:50051":  false, // private interface
		"localhost:50051": false, // hostname
		"50051":           false, // malformed — net.Listen must reject it
		"":                false, // malformed
	}
	for addr, want := range cases {
		if got := grpcBindIsWildcard(addr); got != want {
			t.Errorf("grpcBindIsWildcard(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestReflectionRegistrationOnlyInDevelopment pins the data-leak guard:
// reflection (which publishes the entire service contract) must be absent
// from the server outside development, and present in it.
func TestReflectionRegistrationOnlyInDevelopment(t *testing.T) {
	reflectionService := func(srv *grpc.Server) bool {
		for name := range srv.GetServiceInfo() {
			if strings.HasPrefix(name, "grpc.reflection") {
				return true
			}
		}
		return false
	}

	production := grpc.NewServer()
	captureStdout(t, func() {
		registerGRPCReflection(production, "production", newLogger("production"))
	})
	if reflectionService(production) {
		t.Fatal("reflection must not be registered in production")
	}

	development := grpc.NewServer()
	captureStdout(t, func() {
		registerGRPCReflection(development, "development", newLogger("development"))
	})
	if !reflectionService(development) {
		t.Fatal("reflection must be registered in development")
	}
}
