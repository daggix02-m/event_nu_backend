package service

import (
	"testing"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
)

func testConfig() config.Config {
	return config.Config{
		JWTSecret: "test-secret",
		JWTExpiry: 15 * time.Minute,
	}
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

	sub, err := s.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sub != "u-123" {
		t.Fatalf("expected subject u-123, got %q", sub)
	}
}

func TestParseAccessTokenRejectsExpired(t *testing.T) {
	s := &AuthService{config: testConfig()}
	user := &domain.User{ID: "u-123", Role: "user"}

	token, err := s.signAccessToken(user, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := s.ParseAccessToken(token); err == nil {
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
	if _, err := other.ParseAccessToken(token); err == nil {
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