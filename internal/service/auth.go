package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/daggix02-m/event_nu/backend/internal/config"
	"github.com/daggix02-m/event_nu/backend/internal/domain"
	"github.com/daggix02-m/event_nu/backend/internal/repository"
	"github.com/daggix02-m/event_nu/backend/internal/shared"
	"github.com/daggix02-m/event_nu/backend/internal/validator"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

type AuthService struct {
	users    *repository.UserRepository
	sessions *repository.SessionRepository
	config   config.Config
}

func NewAuthService(users *repository.UserRepository, sessions *repository.SessionRepository, cfg config.Config) *AuthService {
	return &AuthService{users: users, sessions: sessions, config: cfg}
}

// Tokens carries the access (JWT) and refresh (opaque) tokens for a session.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// UserSession pairs an authenticated user with their issued tokens.
type UserSession struct {
	User   *domain.User
	Tokens *Tokens
}

func (s *AuthService) Register(ctx context.Context, email, password, username string, userAgent, ip string) (*UserSession, error) {
	v := validator.New()
	v.Email(email, "email")
	v.MinChars(password, 8, "password")
	v.MaxChars(password, 72, "password")
	v.Required(username, "username")
	v.MinChars(username, 3, "username")
	v.MaxChars(username, 32, "username")
	if !v.Valid() {
		return nil, shared.NewAppError("validation_error", firstFieldError(v), http.StatusUnprocessableEntity)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.users.Create(ctx, strings.ToLower(email), string(hash), username)
	if err != nil {
		return nil, err
	}

	tokens, err := s.newSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, err
	}

	return &UserSession{User: user, Tokens: tokens}, nil
}

func (s *AuthService) Login(ctx context.Context, email, password string, userAgent, ip string) (*UserSession, error) {
	if strings.TrimSpace(email) == "" || password == "" {
		return nil, shared.NewAppError("invalid_credentials", "Invalid email or password.", http.StatusUnauthorized)
	}

	user, err := s.users.GetByEmail(ctx, strings.ToLower(email))
	if err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("invalid_credentials", "Invalid email or password.", http.StatusUnauthorized)
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, shared.NewAppError("invalid_credentials", "Invalid email or password.", http.StatusUnauthorized)
	}

	if user.Status != "active" {
		return nil, shared.NewAppError("account_suspended", "This account is not active.", http.StatusForbidden)
	}

	tokens, err := s.newSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, err
	}

	return &UserSession{User: user, Tokens: tokens}, nil
}

// Refresh rotates a refresh token: validates the existing session, revokes it,
// and issues a new pair.
func (s *AuthService) Refresh(ctx context.Context, refreshToken, userAgent, ip string) (*UserSession, error) {
	hash := hashToken(refreshToken)
	session, err := s.sessions.GetActiveByRefreshHash(ctx, hash)
	if err != nil {
		if err == shared.ErrNotFound {
			return nil, shared.NewAppError("invalid_refresh_token", "The refresh token is invalid or expired.", http.StatusUnauthorized)
		}
		return nil, err
	}

	user, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	// Rotate: the used refresh token can never be replayed.
	if err := s.sessions.Revoke(ctx, hash); err != nil {
		return nil, err
	}

	tokens, err := s.newSession(ctx, user, userAgent, ip)
	if err != nil {
		return nil, err
	}
	return &UserSession{User: user, Tokens: tokens}, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return shared.NewAppError("invalid_refresh_token", "The refresh token is invalid or expired.", http.StatusUnauthorized)
	}
	// Idempotent — revoking an already-revoked token is not an error.
	return s.sessions.Revoke(ctx, hashToken(refreshToken))
}

// GetUser returns a non-deleted user by ID.
func (s *AuthService) GetUser(ctx context.Context, id string) (*domain.User, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AuthService) newSession(ctx context.Context, user *domain.User, userAgent, ip string) (*Tokens, error) {
	refresh, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	accessExpires := now.Add(s.config.JWTExpiry)
	refreshExpires := now.Add(s.config.RefreshTokenExpiry)

	access, err := s.signAccessToken(user, accessExpires)
	if err != nil {
		return nil, err
	}

	session := &domain.Session{
		UserID:      user.ID,
		RefreshHash: hashToken(refresh),
		UserAgent:   truncate(userAgent, 500),
		IPHash:      hashIP(ip),
		DeviceName:  truncate(userAgent, 100),
		ExpiresAt:   refreshExpires,
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}

	return &Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: s.config.JWTExpiry}, nil
}

// signAccessToken mints a signed JWT with sub (user id), role, and jti (session id).
func (s *AuthService) signAccessToken(user *domain.User, expiresAt time.Time) (string, error) {
	claims := jwt.MapClaims{
		"sub":  user.ID,
		"role": string(user.Role),
		"iat":  jwt.NewNumericDate(time.Now()),
		"exp":  jwt.NewNumericDate(expiresAt),
		"jti":  newUUID(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(s.config.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// ParseAccessToken validates a JWT and returns its subject (user id) and role.
func (s *AuthService) ParseAccessToken(tokenString string) (string, string, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.config.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return "", "", err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", "", fmt.Errorf("invalid token claims")
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return "", "", fmt.Errorf("token missing subject")
	}
	role, _ := claims["role"].(string)
	return sub, role, nil
}

func generateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashIP(ip string) string {
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		host = ip
	}
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstFieldError(v *validator.Validator) string {
	for _, msg := range v.FieldErrors {
		return msg
	}
	return "invalid input"
}

// newUUID is a lightweight UUIDv4 generator for token jti claims.
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}