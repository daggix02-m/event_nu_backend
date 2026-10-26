package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeVault mirrors the two HTTP endpoints the client uses: AppRole login
// and one KV record in either v2 or v1 shape. It only implements what the
// client touches so tests fail loudly if the request geometry drifts.
type fakeVault struct {
	mux *http.ServeMux
}

func newFakeVault(t *testing.T, kvVersion string, record map[string]string) *httptest.Server {
	v := &fakeVault{mux: http.NewServeMux()}
	v.mux.HandleFunc("/v1/auth/approle/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RoleID   string `json:"role_id"`
			SecretID string `json:"secret_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.RoleID == "" || in.SecretID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if in.RoleID == "bad" || in.SecretID == "bad" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // fake
			"auth": map[string]any{"client_token": "fake-token"},
		})
	})

	if kvVersion == "v2" {
		v.mux.HandleFunc("/v1/secret/data/eventnu", serveJSON(map[string]any{"data": map[string]any{"data": record}}))
	} else {
		v.mux.HandleFunc("/v1/secret/eventnu", serveJSON(map[string]any{"data": record}))
	}

	srv := httptest.NewServer(v.mux)
	t.Cleanup(srv.Close)
	return srv
}

func serveJSON(shape any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(shape) //nolint:errcheck // fake
	}
}

// setVaultEnv wires a test server into a call context and cleans up every
// variable the loader may export or that a previous test left behind.
func setVaultEnv(t *testing.T, srv *httptest.Server, extra map[string]string) {
	t.Helper()
	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_KV_MOUNT", "secret")
	t.Setenv("VAULT_KV_PATH", "eventnu")
	t.Setenv("VAULT_ROLE_ID", "testing-role")
	t.Setenv("VAULT_SECRET_ID", "testing-secret")
	for k, v := range extra {
		t.Setenv(k, v)
	}
	// Loader-exported variables must not leak between tests/subtests.
	t.Cleanup(func() {
		os.Unsetenv("VAULT-loaded-fresh")
		os.Unsetenv("VAULT-loaded-fresh-2")
	})
}

func TestLoadVaultSecretsDisabledWithoutAddr(t *testing.T) {
	os.Unsetenv("VAULT_ADDR")
	status, err := loadVaultSecrets(context.Background())
	if err != nil {
		t.Fatalf("expected no-op success without VAULT_ADDR, got error: %v", err)
	}
	if status.Loaded != 0 || status.Versions != "" {
		t.Fatalf("expected zero status, got %+v", status)
	}
}

func TestLoadVaultSecretsV2RecordExportsAndPrecedence(t *testing.T) {
	t.Setenv("VAULT-loaded-fresh-2", "side")
	srv := newFakeVault(t, "v2", map[string]string{
		"VAULT-loaded-fresh":   "FROM_VAULT",
		"VAULT-loaded-fresh-2": "FROM_VAULT_BUT_ENV_SET",
	})
	setVaultEnv(t, srv, nil)

	status, err := loadVaultSecrets(context.Background())
	if err != nil {
		t.Fatalf("loadVaultSecrets: %v", err)
	}
	if status.Loaded != 1 {
		t.Fatalf("expected 1 fresh key exported, got %+v", status)
	}
	if status.Skipped != 1 {
		t.Fatalf("expected 1 skipped (env precedence), got %+v", status)
	}
	if got := os.Getenv("VAULT-loaded-fresh"); got != "FROM_VAULT" {
		t.Fatalf("expected Vault value exported, got %q", got)
	}
	if got := os.Getenv("VAULT-loaded-fresh-2"); got != "side" {
		t.Fatalf("expected existing env to win, got %q", got)
	}
}

func TestLoadVaultSecretsV1RecordShape(t *testing.T) {
	srv := newFakeVault(t, "v1", map[string]string{
		"VAULT-loaded-fresh": "FROM_VAULT_V1",
	})
	setVaultEnv(t, srv, nil)
	t.Cleanup(func() { os.Unsetenv("VAULT-loaded-fresh") })

	status, err := loadVaultSecrets(context.Background())
	if err != nil {
		t.Fatalf("loadVaultSecrets: %v", err)
	}
	if status.Versions != "v1" {
		t.Fatalf("expected detected kv version v1, got %q", status.Versions)
	}
	if got := os.Getenv("VAULT-loaded-fresh"); got != "FROM_VAULT_V1" {
		t.Fatalf("expected v1 record exported, got %q", got)
	}
}

func TestLoadVaultSecretsLoginFailureFailsFast(t *testing.T) {
	srv := newFakeVault(t, "v2", nil)
	setVaultEnv(t, srv, map[string]string{"VAULT_ROLE_ID": "bad"})

	if _, err := loadVaultSecrets(context.Background()); err == nil {
		t.Fatal("expected hard error on rejected AppRole credentials")
	}
}

func TestLoadVaultSecretsMissingRecordFailsFast(t *testing.T) {
	srv := newFakeVault(t, "v2", nil)
	setVaultEnv(t, srv, nil)
	// Point the path beyond any stubbed route.
	t.Setenv("VAULT_KV_PATH", "absent")

	if _, err := loadVaultSecrets(context.Background()); err == nil {
		t.Fatal("expected hard error when the KV record is missing")
	}
}

func TestLoadVaultSecretsMissingCredentialsFailsFast(t *testing.T) {
	srv := newFakeVault(t, "v2", nil)
	setVaultEnv(t, srv, nil)
	t.Setenv("VAULT_ROLE_ID", "")

	if _, err := loadVaultSecrets(context.Background()); err == nil {
		t.Fatal("expected hard error when VAULT_ROLE_ID is missing alongside VAULT_ADDR")
	}
}

func TestLoadVaultSecretsSecretIDFromFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret-id")
	if err := os.WriteFile(file, []byte("testing-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newFakeVault(t, "v2", map[string]string{
		"VAULT-loaded-fresh": "FROM_VAULT",
	})
	setVaultEnv(t, srv, nil)
	t.Setenv("VAULT_SECRET_ID", "")
	t.Setenv("VAULT_SECRET_ID_FILE", file)
	t.Cleanup(func() { os.Unsetenv("VAULT-loaded-fresh") })

	if _, err := loadVaultSecrets(context.Background()); err != nil {
		t.Fatalf("loadVaultSecrets with VAULT_SECRET_ID_FILE: %v", err)
	}
	if got := os.Getenv("VAULT-loaded-fresh"); got != "FROM_VAULT" {
		t.Fatalf("expected Vault value exported via file-auth path, got %q", got)
	}
}

func TestLoadVaultSecretsRejectsTraversalPath(t *testing.T) {
	srv := newFakeVault(t, "v2", nil)
	setVaultEnv(t, srv, nil)
	t.Setenv("VAULT_KV_PATH", "../users")

	if _, err := loadVaultSecrets(context.Background()); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("expected traversal rejection error, got: %v", err)
	}
}

func TestLoadVaultSecretsEmptyRecordFailsFast(t *testing.T) {
	srv := newFakeVault(t, "v1", nil) // v1 stub serves an empty data object
	setVaultEnv(t, srv, nil)

	if _, err := loadVaultSecrets(context.Background()); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-record rejection, got: %v", err)
	}
}
