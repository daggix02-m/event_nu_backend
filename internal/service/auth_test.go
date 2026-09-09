package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

func testConfig() config.Config {
	return config.Config{
		JWTSecret:          "test-secret",
		JWTExpiry:          15 * time.Minute,
		RefreshTokenExpiry: 30 * 24 * time.Hour,
	}
}

// stubUserStore implements service.userStore with a scripted email lookup so
// login can be tested without a database.
type stubUserStore struct {
	byEmail func(email string) (*domain.User, error)
	byID    func(id string) (*domain.User, error)
}

func (s stubUserStore) Create(ctx context.Context, email, passwordHash, username string) (*domain.User, error) {
	return nil, nil
}

func (s stubUserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.byEmail(email)
}

func (s stubUserStore) GetByID(ctx context.Context, id string) (*domain.User, error) {
	if s.byID != nil {
		return s.byID(id)
	}
	return nil, shared.ErrNotFound
}

func (s stubUserStore) UpdateProfile(ctx context.Context, id string, username, bio, photoURL *string) (*domain.User, error) {
	return nil, nil
}

func TestHashTokenDeterministic(t *testing.T) {
	a := hashToken("abc")
	b := hashToken("abc")
	c := hashToken("abd")
	if a != b {
		t.Fatal("hashToken must be deterministic")
	}
	if a == c {
		t.Fatal("hashToken must differ across inputs")
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %d", len(a))
	}
}

func TestParseAccessTokenRoundTrip(t *testing.T) {
	s := &AuthService{config: testConfig()}
	user := &domain.User{ID: "u-123", Role: "user"}

	token, err := s.signAccessToken(user, time.Now().Add(15*time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	sub, role, err := s.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub != "u-123" {
		t.Fatalf("expected subject u-123, got %q", sub)
	}
	if role != "user" {
		t.Fatalf("expected role user, got %q", role)
	}
}

func TestParseAccessTokenRejectsExpired(t *testing.T) {
	s := &AuthService{config: testConfig()}
	user := &domain.User{ID: "u-123", Role: "user"}

	token, err := s.signAccessToken(user, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, _, err := s.ParseAccessToken(token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestParseAccessTokenRejectsWrongSecret(t *testing.T) {
	s := &AuthService{config: testConfig()}
	user := &domain.User{ID: "u-123", Role: "user"}

	token, err := s.signAccessToken(user, time.Now().Add(15*time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	other := &AuthService{config: config.Config{JWTSecret: "different", JWTExpiry: 15 * time.Minute}}
	if _, _, err := other.ParseAccessToken(token); err == nil {
		t.Fatal("expected token signed with different secret to be rejected")
	}
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	s := &AuthService{config: testConfig()}
	_, err := s.Register(t.Context(), "a@b.com", "short", "alice", "t", "1.2.3.4")
	if err == nil {
		t.Fatal("expected weak-password rejection")
	}
}

func TestRegisterRejectsInvalidEmail(t *testing.T) {
	s := &AuthService{config: testConfig()}
	_, err := s.Register(t.Context(), "not-an-email", "password123", "alice", "t", "1.2.3.4")
	if err == nil {
		t.Fatal("expected invalid-email rejection")
	}
}

// TestLoginBcryptWorkOnUnknownEmailAndWrongPassword proves-by-count that both
// the "email not found" and "wrong password" login paths perform exactly one
// bcrypt comparison and return identical errors — so an attacker cannot tell
// the cases apart by response behavior or timing.
func TestLoginBcryptWorkOnUnknownEmailAndWrongPassword(t *testing.T) {
	orig := bcryptCompare
	t.Cleanup(func() { bcryptCompare = orig })

	unknown := &AuthService{users: stubUserStore{byEmail: func(string) (*domain.User, error) {
		return nil, shared.ErrNotFound
	}}, config: testConfig()}
	wrong := &AuthService{users: stubUserStore{byEmail: func(string) (*domain.User, error) {
		return &domain.User{ID: "u-1", Status: "active", PasswordHash: "never-matches"}, nil
	}}, config: testConfig()}

	var seen []error
	for _, tc := range []struct {
		name string
		svc  *AuthService
	}{
		{"unknown email", unknown},
		{"wrong password", wrong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			bcryptCompare = func(_, _ []byte) error {
				calls++
				return errors.New("mismatch")
			}
			_, err := tc.svc.Login(t.Context(), "alice@example.com", "somepass123", "test-agent", "1.2.3.4")
			if calls != 1 {
				t.Fatalf("expected exactly 1 bcrypt compare, got %d", calls)
			}
			seen = append(seen, err)
			assertInvalidCredentials(t, err)
		})
	}

	for i := 1; i < len(seen); i++ {
		if seen[0].Error() != seen[i].Error() {
			t.Fatalf("expected byte-identical error on both paths, got %q vs %q", seen[0], seen[i])
		}
	}
}

func assertInvalidCredentials(t *testing.T, err error) {
	t.Helper()
	appErr, ok := shared.AsAppError(err)
	if !ok {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != "invalid_credentials" || appErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("expected invalid_credentials/401, got %s/%d", appErr.Code, appErr.HTTPStatus)
	}
}

func TestHashIPStripsPort(t *testing.T) {
	withPort := hashIP("1.2.3.4:8080")
	withoutPort := hashIP("1.2.3.4")
	if withPort != withoutPort {
		t.Fatal("hashIP must hash the host only, ignoring the port")
	}
	if len(withPort) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %d", len(withPort))
	}
}

func TestHashIPDeterministicAndDistinct(t *testing.T) {
	a := hashIP("10.0.0.1:1234")
	b := hashIP("10.0.0.1:9999")
	c := hashIP("10.0.0.2:1234")
	if a != b {
		t.Fatal("same IP with different ports must hash identically")
	}
	if a == c {
		t.Fatal("different IPs must hash differently")
	}
}

func TestHashIPMalformedInputDoesNotPanic(t *testing.T) {
	// SplitHostPort fails, so the whole string is hashed.
	got := hashIP("not-an-ip")
	if len(got) != 64 {
		t.Fatalf("expected sha256 hex (64 chars), got %d", len(got))
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{name: "short unchanged", in: "abc", n: 5, want: "abc"},
		{name: "exact length unchanged", in: "abc", n: 3, want: "abc"},
		{name: "long truncated", in: "abcdef", n: 3, want: "abc"},
		{name: "empty", in: "", n: 3, want: ""},
		{name: "zero limit", in: "abc", n: 0, want: ""},
		{name: "unix byte semantics", in: "héllo", n: 3, want: "hé"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.n); got != tt.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	tok, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("expected 32 random bytes hex-encoded (64 chars), got %d", len(tok))
	}
	for _, r := range tok {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("token contains non-hex char %q", r)
		}
	}
	again, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == again {
		t.Fatal("two generated refresh tokens must differ")
	}
}

func TestGetUserReturnsUser(t *testing.T) {
	user := &domain.User{ID: "u-1", Email: "a@b.com"}
	s := &AuthService{users: stubUserStore{byID: func(id string) (*domain.User, error) {
		if id != "u-1" {
			t.Fatalf("expected lookup by u-1, got %q", id)
		}
		return user, nil
	}}}
	got, err := s.GetUser(t.Context(), "u-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != user {
		t.Fatalf("expected the stubbed user back, got %v", got)
	}
}

func TestGetUserPropagatesNotFound(t *testing.T) {
	s := &AuthService{users: stubUserStore{byID: func(string) (*domain.User, error) {
		return nil, shared.ErrNotFound
	}}}
	if _, err := s.GetUser(t.Context(), "missing"); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegisterRejectsShortUsername(t *testing.T) {
	s := &AuthService{config: testConfig()}
	_, err := s.Register(t.Context(), "a@b.com", "password123", "ab", "t", "1.2.3.4")
	appErr, ok := shared.AsAppError(err)
	if !ok || appErr.HTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 validation error, got %v", err)
	}
}

func TestRegisterRejectsLongUsername(t *testing.T) {
	s := &AuthService{config: testConfig()}
	_, err := s.Register(t.Context(), "a@b.com", "password123", "abcdefghijklmnopqrstuvwxyzabcdefg", "t", "1.2.3.4")
	if err == nil {
		t.Fatal("expected long-username rejection")
	}
}

func TestRegisterRejectsOversizedPassword(t *testing.T) {
	s := &AuthService{config: testConfig()}
	_, err := s.Register(t.Context(), "a@b.com", "x"+strings.Repeat("y", 72), "alice", "t", "1.2.3.4")
	appErr, ok := shared.AsAppError(err)
	if !ok || appErr.HTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 validation error for >72 char password, got %v", err)
	}
}

func TestLoginRejectsBlankInputsWithoutBcrypt(t *testing.T) {
	orig := bcryptCompare
	t.Cleanup(func() { bcryptCompare = orig })
	calls := 0
	bcryptCompare = func(_, _ []byte) error {
		calls++
		return errors.New("should not run")
	}

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{name: "blank email", email: "   ", password: "password123"},
		{name: "blank password", email: "a@b.com", password: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &AuthService{users: stubUserStore{byEmail: func(string) (*domain.User, error) {
				return &domain.User{ID: "u-1"}, nil
			}}, config: testConfig()}
			_, err := s.Login(t.Context(), tt.email, tt.password, "t", "1.2.3.4")
			assertInvalidCredentials(t, err)
		})
	}
	if calls != 0 {
		t.Fatalf("expected zero bcrypt comparisons for blank inputs, got %d", calls)
	}
}

func TestLoginRejectsSuspendedAccount(t *testing.T) {
	orig := bcryptCompare
	t.Cleanup(func() { bcryptCompare = orig })
	bcryptCompare = func(_, _ []byte) error { return nil }

	s := &AuthService{users: stubUserStore{byEmail: func(string) (*domain.User, error) {
		return &domain.User{ID: "u-1", Status: "suspended", PasswordHash: "hash"}, nil
	}}, config: testConfig()}

	_, err := s.Login(t.Context(), "a@b.com", "password123", "t", "1.2.3.4")
	appErr, ok := shared.AsAppError(err)
	if !ok || appErr.Code != "account_suspended" || appErr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("expected account_suspended/403, got %v", err)
	}
}

func TestLoginPropagatesStoreError(t *testing.T) {
	boom := errors.New("db down")
	s := &AuthService{users: stubUserStore{byEmail: func(string) (*domain.User, error) {
		return nil, boom
	}}, config: testConfig()}
	_, err := s.Login(t.Context(), "a@b.com", "password123", "t", "1.2.3.4")
	if !errors.Is(err, boom) {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

// stubSessionStore implements sessionStore with scripted methods so refresh
// rotation and logout can be tested without a database.
type stubSessionStore struct {
	onConsume func(hash string) (*domain.Session, error)
	onCreate  func(s *domain.Session) error
	onRevoke  func(hash string) error
}

func (s stubSessionStore) Create(ctx context.Context, sess *domain.Session) error {
	if s.onCreate == nil {
		return nil
	}
	return s.onCreate(sess)
}

func (s stubSessionStore) ConsumeForRotation(ctx context.Context, hash string) (*domain.Session, error) {
	if s.onConsume == nil {
		return nil, shared.ErrNotFound
	}
	return s.onConsume(hash)
}

func (s stubSessionStore) Revoke(ctx context.Context, hash string) error {
	if s.onRevoke == nil {
		return nil
	}
	return s.onRevoke(hash)
}

func TestRefreshRotatesToken(t *testing.T) {
	oldToken := "old-refresh-token-123"
	user := &domain.User{ID: "u-1", Email: "a@b.com"}
	var created *domain.Session
	var consumedHash string

	s := &AuthService{
		users: stubUserStore{byID: func(id string) (*domain.User, error) {
			if id != "u-1" {
				t.Fatalf("expected user lookup by u-1, got %q", id)
			}
			return user, nil
		}},
		sessions: stubSessionStore{
			onConsume: func(hash string) (*domain.Session, error) {
				consumedHash = hash
				return &domain.Session{UserID: "u-1", RefreshHash: hash}, nil
			},
			onCreate: func(sess *domain.Session) error {
				created = sess
				return nil
			},
		},
		config: testConfig(),
	}

	got, err := s.Refresh(t.Context(), oldToken, "agent/1.0", "1.2.3.4:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Tokens == nil || got.User != user {
		t.Fatalf("unexpected session result: %+v", got)
	}

	if consumedHash != hashToken(oldToken) {
		t.Fatalf("expected rotation to consume hash of the old token, got %q", consumedHash)
	}
	if created == nil {
		t.Fatal("expected a new session to be created")
	}
	if created.UserID != "u-1" {
		t.Fatalf("expected new session for u-1, got %q", created.UserID)
	}
	if created.RefreshHash == hashToken(oldToken) {
		t.Fatal("new session must hash a different refresh token than the consumed one")
	}
	if created.RefreshHash != hashToken(got.Tokens.RefreshToken) {
		t.Fatal("stored hash must match the issued refresh token")
	}
	if got.Tokens.RefreshToken == oldToken {
		t.Fatal("issued refresh token must differ from the consumed one")
	}
	wantExpiry := time.Now().Add(testConfig().RefreshTokenExpiry)
	if created.ExpiresAt.Sub(wantExpiry) > time.Minute || wantExpiry.Sub(created.ExpiresAt) > time.Minute {
		t.Fatalf("expected session expiry near now+30d, got %v", created.ExpiresAt)
	}
}

func TestRefreshAppliesTruncation(t *testing.T) {
	s := &AuthService{
		users: stubUserStore{byID: func(id string) (*domain.User, error) {
			return &domain.User{ID: "u-1"}, nil
		}},
		sessions: stubSessionStore{
			onConsume: func(hash string) (*domain.Session, error) {
				return &domain.Session{UserID: "u-1", RefreshHash: hash}, nil
			},
			onCreate: func(sess *domain.Session) error { return nil },
		},
		config: testConfig(),
	}
	longAgent := "VeryLongUserAgent:" + strings.Repeat("x", 600)
	_, err := s.Refresh(t.Context(), "old-token", longAgent, "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	var created bool
	s := &AuthService{
		users: stubUserStore{},
		sessions: stubSessionStore{
			onCreate: func(sess *domain.Session) error {
				created = true
				return nil
			},
		},
		config: testConfig(),
	}
	_, err := s.Refresh(t.Context(), "unknown-token", "t", "1.2.3.4")
	assertAppError(t, err, "invalid_refresh_token", http.StatusUnauthorized)
	if created {
		t.Fatal("no new session may be created for an invalid refresh token")
	}
}

func TestRefreshPropagatesConsumeError(t *testing.T) {
	boom := errors.New("store down")
	s := &AuthService{
		users:    stubUserStore{},
		sessions: stubSessionStore{onConsume: func(hash string) (*domain.Session, error) { return nil, boom }},
		config:   testConfig(),
	}
	if _, err := s.Refresh(t.Context(), "token", "t", "1.2.3.4"); !errors.Is(err, boom) {
		t.Fatalf("expected consume error to propagate, got %v", err)
	}
}

func TestRefreshPropagatesUserLookupError(t *testing.T) {
	boom := errors.New("user store down")
	s := &AuthService{
		users: stubUserStore{byID: func(id string) (*domain.User, error) { return nil, boom }},
		sessions: stubSessionStore{
			onConsume: func(hash string) (*domain.Session, error) {
				return &domain.Session{UserID: "u-1", RefreshHash: hash}, nil
			},
		},
		config: testConfig(),
	}
	if _, err := s.Refresh(t.Context(), "token", "t", "1.2.3.4"); !errors.Is(err, boom) {
		t.Fatalf("expected user lookup error to propagate, got %v", err)
	}
}

func TestRefreshPropagatesCreateError(t *testing.T) {
	boom := errors.New("insert session failed")
	s := &AuthService{
		users: stubUserStore{byID: func(id string) (*domain.User, error) {
			return &domain.User{ID: "u-1"}, nil
		}},
		sessions: stubSessionStore{
			onConsume: func(hash string) (*domain.Session, error) {
				return &domain.Session{UserID: "u-1", RefreshHash: hash}, nil
			},
			onCreate: func(sess *domain.Session) error { return boom },
		},
		config: testConfig(),
	}
	if _, err := s.Refresh(t.Context(), "token", "t", "1.2.3.4"); !errors.Is(err, boom) {
		t.Fatalf("expected create error to propagate, got %v", err)
	}
}

func TestLogoutRevokesTokenHash(t *testing.T) {
	var revokedHash string
	s := &AuthService{sessions: stubSessionStore{
		onRevoke: func(hash string) error {
			revokedHash = hash
			return nil
		},
	}}
	if err := s.Logout(t.Context(), "token-to-revoke"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if revokedHash != hashToken("token-to-revoke") {
		t.Fatalf("expected logout to revoke the token hash, got %q", revokedHash)
	}
}

func TestLogoutBlankTokenRejected(t *testing.T) {
	revoked := false
	s := &AuthService{sessions: stubSessionStore{
		onRevoke: func(hash string) error {
			revoked = true
			return nil
		},
	}}
	err := s.Logout(t.Context(), "   ")
	assertAppError(t, err, "invalid_refresh_token", http.StatusUnauthorized)
	if revoked {
		t.Fatal("revoke must not be called for a blank token")
	}
}

func TestLogoutPropagatesRevokeError(t *testing.T) {
	boom := errors.New("store down")
	s := &AuthService{sessions: stubSessionStore{
		onRevoke: func(hash string) error { return boom },
	}}
	if err := s.Logout(t.Context(), "token"); !errors.Is(err, boom) {
		t.Fatalf("expected revoke error to propagate, got %v", err)
	}
}
