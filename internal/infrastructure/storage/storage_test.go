package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFactoryDefaultsToLocal(t *testing.T) {
	store, err := New("", "", "", "", "", "", "http://cdn.example", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := store.(*Local); !ok {
		t.Fatalf("expected *Local, got %T", store)
	}
	if store.Bucket() != "local" {
		t.Fatalf("Bucket() = %q, want local", store.Bucket())
	}
}

func TestFactoryLocalCaseInsensitive(t *testing.T) {
	store, err := New("LOCAL", "", "", "", "", "", "http://cdn.example", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := store.(*Local); !ok {
		t.Fatalf("expected *Local, got %T", store)
	}
}

func TestFactoryS3(t *testing.T) {
	store, err := New("s3", "http://minio.local:9000", "us-east-1", "media",
		"ak", "sk", "http://cdn.example/", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s3, ok := store.(*S3)
	if !ok {
		t.Fatalf("expected *S3, got %T", store)
	}
	if s3.Bucket() != "media" {
		t.Fatalf("Bucket() = %q, want media", s3.Bucket())
	}
	// PublicReadURL() is deterministic and does not touch the network.
	if got := s3.PublicReadURL("a b/c.png"); got != "http://cdn.example/media/objects/a%20b%2Fc.png" {
		t.Fatalf("PublicReadURL() = %q", got)
	}
}

func TestFactoryRejectsUnknownProvider(t *testing.T) {
	if _, err := New("ftp", "", "", "", "", "", "", t.TempDir()); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// newLocalStore returns a Local provider rooted in a temp dir with a base URL
// that intentionally carries a trailing slash to verify trimming.
func newLocalStore(t *testing.T) *Local {
	t.Helper()
	return NewLocal(t.TempDir(), "http://cdn.example/")
}

func TestLocalUploadDownloadDeleteRoundTrip(t *testing.T) {
	ctx := context.Background()
	l := newLocalStore(t)
	key := "events/abc-123/original.png"
	payload := []byte("fake image bytes")

	if err := l.Upload(ctx, key, bytes.NewReader(payload), "image/png", int64(len(payload))); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, err := os.Stat(l.ObjectPath(key)); err != nil {
		t.Fatalf("object file not created: %v", err)
	}

	rc, size, err := l.Download(ctx, key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes mismatch: %q", got)
	}

	if err := l.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := l.Download(ctx, key); err == nil {
		t.Fatal("Download after Delete must fail")
	}
}

func TestLocalUploadCreatesNestedDirectories(t *testing.T) {
	ctx := context.Background()
	l := newLocalStore(t)
	key := "a/b/c/d/e.bin"
	if err := l.Upload(ctx, key, strings.NewReader("x"), "application/octet-stream", 1); err != nil {
		t.Fatalf("Upload nested: %v", err)
	}
	if _, err := os.Stat(l.ObjectPath(key)); err != nil {
		t.Fatalf("nested object not created: %v", err)
	}
}

func TestLocalDeleteMissingIsNoop(t *testing.T) {
	if err := newLocalStore(t).Delete(context.Background(), "does-not-exist.bin"); err != nil {
		t.Fatalf("Delete missing must succeed, got %v", err)
	}
}

func TestLocalPresignUploadPointsAtPublicURL(t *testing.T) {
	ctx := context.Background()
	l := newLocalStore(t)
	key := "events/x/o.jpg"
	got, err := l.PresignUpload(ctx, key, "image/jpeg", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	want := l.PublicReadURL(key)
	if got != want {
		t.Fatalf("PresignUpload = %q, want %q", got, want)
	}
}

func TestLocalPublicReadURLEscapesKeyAndTrimsSlash(t *testing.T) {
	l := newLocalStore(t)
	if got := l.PublicReadURL("a b/c.png"); got != "http://cdn.example/media/objects/a%20b%2Fc.png" {
		t.Fatalf("PublicReadURL() = %q", got)
	}
}

// TestLocalSafeKeyRejectsTraversal drives the provider's defense-in-depth check
// directly. Go's http.ServeMux cleans path segments before routing, but the
// handler must never write outside the store directory regardless.
func TestLocalSafeKeyRejectsTraversal(t *testing.T) {
	l := newLocalStore(t)
	bad := []string{
		"",
		"..",
		"../etc/passwd",
		"a/../../b.png",
	}
	for _, key := range bad {
		req := httptest.NewRequest(http.MethodGet, "/media/objects/x", nil)
		req.SetPathValue("key", key)
		if _, err := l.safeKey(req); err == nil {
			t.Errorf("safeKey(%q) must fail, got nil", key)
		}
	}

	// Keys that stay inside the store directory are accepted verbatim, even if
	// they contain dots or a leading slash (filepath.Join nests the key under
	// the store root either way).
	good := []struct{ in, want string }{
		{"a/b/c.png", "a/b/c.png"},
		{"nested/dir/o x.png", "nested/dir/o x.png"},
		{".../up", ".../up"},
		{"/etc/passwd", "/etc/passwd"},
	}
	for _, g := range good {
		req := httptest.NewRequest(http.MethodGet, "/media/objects/x", nil)
		req.SetPathValue("key", g.in)
		got, err := l.safeKey(req)
		if err != nil {
			t.Errorf("safeKey(%q) failed: %v", g.in, err)
		}
		if got != g.want {
			t.Errorf("safeKey(%q) = %q, want %q", g.in, got, g.want)
		}
	}
}

func TestLocalServeObject(t *testing.T) {
	ctx := context.Background()
	l := newLocalStore(t)
	key := "events/abc/o.png"
	if err := l.Upload(ctx, key, strings.NewReader("serve me"), "image/png", 8); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /media/objects/{key...}", http.HandlerFunc(l.ServeObject))

	tt := []struct {
		name   string
		key    string
		status int
		body   string
	}{
		{name: "existing object", key: key, status: 200, body: "serve me"},
		{name: "missing object", key: "nope/x.png", status: 404},
		{name: "missing key", key: "", status: 404},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/media/objects/"+tc.key, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.status, rec.Body.String())
			}
			if tc.body != "" && rec.Body.String() != tc.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.body)
			}
		})
	}
}

func TestLocalStoreObject(t *testing.T) {
	l := newLocalStore(t)
	const maxBytes = 16

	mux := http.NewServeMux()
	mux.Handle("PUT /media/objects/{key...}", l.StoreObject(maxBytes))

	tt := []struct {
		name        string
		key         string
		body        string
		contentLen  int64
		status      int
		expectStore bool
	}{
		{name: "valid upload", key: "u/v.png", body: "0123456789", contentLen: 10, status: 204, expectStore: true},
		{name: "missing key", key: "", body: "x", contentLen: 1, status: 404},
		{name: "oversized rejected", key: "u/big.png", body: strings.Repeat("x", 32), contentLen: 32, status: 413},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/media/objects/"+tc.key, strings.NewReader(tc.body))
			req.ContentLength = tc.contentLen
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.status, rec.Body.String())
			}
			if tc.expectStore {
				if _, err := os.Stat(l.ObjectPath(tc.key)); err != nil {
					t.Fatalf("object not stored: %v", err)
				}
			}
		})
	}
}
