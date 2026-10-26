package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Vault integration (KV secrets via AppRole) — enabled by setting
// VAULT_ADDR. Semantics:
//
//   - VAULT_ADDR unset  : behavior is unchanged (pure environment).
//   - VAULT_ADDR set    : Load fails fast on ANY Vault error. A partially
//     working Vault would otherwise move secret failures from boot
//     (loud, obvious) to first login attempt (quiet, confusing).
//
// Precedence stays "environment wins": every record key is exported as an
// environment variable only when that variable is not already set, so an
// operator can still override a Vault value per-execution for debugging.
// The record lives at VAULT_KV_MOUNT/VAULT_KV_PATH (default secret/eventnu)
// with one key per config variable (DATABASE_URL, JWT_SECRET, ...).
//
// The client is hand-rolled HTTP like every other outbound client in this
// repo (brevo.go, fcm.go, chapa.go): explicit timeout, no SDK dependency,
// and errors are always redacted — no secret values, tokens, or URLs with
// embedded credentials ever reach a log line.
const (
	vaultLoginPath    = "/v1/auth/approle/login"
	vaultRequestLimit = 256 << 10 // response body cap; records are small
	vaultTimeout      = 10 * time.Second
)

type vaultClient struct {
	addr   string // no trailing slash
	mount  string // KV mount, e.g. "secret"
	path   string // record path within the mount, e.g. "eventnu"
	client *http.Client
}

// VaultStatus summarizes one Load() run for the startup log.
type VaultStatus struct {
	Loaded   int // keys exported from the KV record
	Skipped  int // keys already set in the environment (env precedence)
	Versions string
}

// loadVaultSecrets is the Load() entry point. No-op when VAULT_ADDR is
// unset; returns a fail-fast error otherwise.
func loadVaultSecrets(ctx context.Context) (VaultStatus, error) {
	addr := strings.TrimRight(os.Getenv("VAULT_ADDR"), "/")
	if addr == "" {
		return VaultStatus{}, nil
	}
	mount := getEnv("VAULT_KV_MOUNT", "secret")
	path := getEnv("VAULT_KV_PATH", "eventnu")
	if path == "" || strings.Contains(path, "..") {
		return VaultStatus{}, fmt.Errorf("VAULT_KV_PATH must be a non-empty relative KV path (no traversal)")
	}

	vc := &vaultClient{
		addr:   addr,
		mount:  mount,
		path:   path,
		client: &http.Client{Timeout: vaultTimeout},
	}

	token, err := vc.login(ctx)
	if err != nil {
		return VaultStatus{}, fmt.Errorf("vault AppRole login failed: %w", err)
	}

	data, version, err := vc.readRecord(ctx, token)
	if err != nil {
		return VaultStatus{}, fmt.Errorf("vault KV read failed: %w", err)
	}

	status := VaultStatus{Versions: version}
	for key, raw := range data {
		value, ok := raw.(string)
		if !ok {
			return VaultStatus{}, fmt.Errorf("vault KV record %q: key %q is not a string", vc.fullPath(), key)
		}
		if _, exists := os.LookupEnv(key); exists {
			status.Skipped++
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return VaultStatus{}, fmt.Errorf("vault: could not export %q: %w", key, err)
		}
		status.Loaded++
	}
	if status.Loaded+status.Skipped == 0 {
		return VaultStatus{}, fmt.Errorf("vault KV record %q is empty; refusing to continue with no secrets", vc.fullPath())
	}
	slog.Info("vault secrets loaded",
		"addr", addr,
		"record", vc.fullPath(),
		"loaded", status.Loaded,
		"env_precedence_skipped", status.Skipped,
		"kv_version", version,
	)
	return status, nil
}

// login exchanges role_id + secret_id for a client token. The secret id is
// use-limited by Vault; exactly one request is made, no blind retries.
func (vc *vaultClient) login(ctx context.Context) (string, error) {
	roleID := os.Getenv("VAULT_ROLE_ID")
	secretID := os.Getenv("VAULT_SECRET_ID")
	if f := os.Getenv("VAULT_SECRET_ID_FILE"); secretID == "" && f != "" {
		raw, err := os.ReadFile(f) // compose secrets-style: file perms are the boundary
		if err != nil {
			return "", fmt.Errorf("VAULT_SECRET_ID_FILE: %w", err)
		}
		secretID = strings.TrimSpace(string(raw))
	}
	if roleID == "" || secretID == "" {
		return "", errors.New("VAULT_ROLE_ID and VAULT_SECRET_ID (or VAULT_SECRET_ID_FILE) are required when VAULT_ADDR is set")
	}

	body := fmt.Sprintf(
		`{"role_id":%q,"secret_id":%q}`,
		roleID, secretID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vc.addr+vaultLoginPath, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := vc.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, vaultRequestLimit))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d (AppRole credentials rejected or Vault unreachable to apps; secret id may be consumed — inspect Vault audit logs, never app logs here)", resp.StatusCode)
	}
	var parsed struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("unrecognized login response: %w", err)
	}
	if parsed.Auth.ClientToken == "" {
		return "", errors.New("login response carried no client token")
	}
	return parsed.Auth.ClientToken, nil
}

// readRecord reads one KV record and normalizes to a key/value map. KV v2
// (default for a fresh `vault operator init`) exposes records under a
// `data/` prefix and nests values under data.data; KV v1 exposes them
// directly. Version is detected by response shape, so a mount migrated
// between versions does not require a config change.
func (vc *vaultClient) readRecord(ctx context.Context, token string) (map[string]any, string, error) {
	body, status, err := vc.getRequest(ctx, token, vc.addr+"/v1/"+vc.mount+"/data/"+vc.path)
	if err != nil {
		return nil, "", err
	}
	var v2 struct {
		Data struct {
			Data map[string]any
		}
	}
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &v2); err != nil {
			return nil, "", fmt.Errorf("v2 record %q returned unrecognizable shape: %w", vc.dataPath("data/"), err)
		}
		if v2.Data.Data != nil {
			return v2.Data.Data, "v2", nil
		}
		// nil inner data: either a stored-empty record or a v1-shaped body
		// at the v2 URL — probe the v1 endpoint before giving up.
	}
	body, status, err = vc.getRequest(ctx, token, vc.addr+"/v1/"+vc.mount+"/"+vc.path)
	if err != nil {
		return nil, "", err
	}
	var v1 struct {
		Data map[string]any
	}
	if status == http.StatusOK {
		if err := json.Unmarshal(body, &v1); err != nil {
			return nil, "", fmt.Errorf("v1 record %q returned unrecognizable shape: %w", vc.dataPath(""), err)
		}
		if v1.Data == nil {
			v1.Data = map[string]any{} // stored-empty record; caller's empty check rejects it
		}
		return v1.Data, "v1", nil
	}
	return nil, "", fmt.Errorf("record %q not found (status %d) — seed it or fix VAULT_KV_PATH", vc.fullPath(), status)
}

func (vc *vaultClient) getRequest(ctx context.Context, token, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Vault-Token", token)
	resp, err := vc.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, vaultRequestLimit))
	if err != nil {
		return nil, 0, err
	}
	return body, resp.StatusCode, nil
}

// buildFullPath is safe for error strings: no values, only address + path.
func (vc *vaultClient) fullPath() string {
	return vc.mount + "/" + vc.path
}

// dataPath exists only for error-message consistency between versions.
func (vc *vaultClient) dataPath(prefix string) string {
	return vc.mount + "/" + prefix + vc.path
}
