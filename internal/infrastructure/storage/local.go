package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/daggix02-m/event_nu_backend/internal/domain"
)

// Compile-time proof that the repository adapter implements the core's
// driven port (hexagonal: adapters depend on ports, never the reverse).
var _ domain.ObjectStore = (*Local)(nil)

type Local struct {
	dir        string
	publicBase string
}

func NewLocal(dir, publicBase string) *Local {
	return &Local{dir: dir, publicBase: strings.TrimSuffix(publicBase, "/")}
}

func (l *Local) Bucket() string { return "local" }

func (l *Local) ObjectPath(key string) string {
	return filepath.Join(l.dir, filepath.FromSlash(key))
}

func (l *Local) PresignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	return l.PublicReadURL(key), nil
}

func (l *Local) PublicReadURL(key string) string {
	return l.publicBase + "/media/objects/" + url.PathEscape(key)
}

func (l *Local) Upload(ctx context.Context, key string, r io.Reader, contentType string, sizeBytes int64) error {
	dst := l.ObjectPath(key)
	if err := os.MkdirAll(path.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("local mkdir: %w", err)
	}
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("local create: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("local write: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("local close: %w", err)
	}
	return nil
}

func (l *Local) Download(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	f, err := os.Open(l.ObjectPath(key))
	if err != nil {
		return nil, 0, fmt.Errorf("local open: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("local stat: %w", err)
	}
	return f, st.Size(), nil
}

func (l *Local) Delete(ctx context.Context, key string) error {
	err := os.Remove(l.ObjectPath(key))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local delete: %w", err)
	}
	return nil
}

func (l *Local) ServeObject(w http.ResponseWriter, r *http.Request) {
	key, err := l.safeKey(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(l.ObjectPath(key))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path.Base(key), st.ModTime(), f)
}

func (l *Local) StoreObject(maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, err := l.safeKey(r)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength > maxBytes {
			http.Error(w, "object too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := l.Upload(r.Context(), key, http.MaxBytesReader(w, r.Body, maxBytes), r.Header.Get("Content-Type"), r.ContentLength); err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (l *Local) safeKey(r *http.Request) (string, error) {
	key := chi.URLParam(r, "*")
	if key == "" {
		return "", fmt.Errorf("missing object key")
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	root := filepath.Clean(l.dir)
	rel, err := filepath.Rel(root, filepath.Join(root, clean))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe object key %q", key)
	}
	return filepath.ToSlash(clean), nil
}
