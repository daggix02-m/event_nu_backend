package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
)

func testConfig() config.Config {
	return config.Config{
		JWTSecret: "test-secret",
		JWTExpiry: 15 * time.Minute,
	}
}

// stubUserStore implements service.userStore with a scripted email lookup so
// login can be tested without a database.
type stubUserStore struct {
	byEmail func(email string) (*domain.User, error)
}

func (s stubUserStore) Create(ctx context.Context, email, passwordHash, username string) (*domain.User, error) {
	return nil, nil
}

func (s stubUserStore) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return s.byEmail(email)
}

func (s stubUserStore) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return nil, shared.ErrNotFound
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
