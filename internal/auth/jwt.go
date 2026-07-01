package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	tokenIssuer     = "ecv4"
	accessAudience  = "ecv4-access"
	refreshAudience = "ecv4-refresh"
)

// TokenService issues and verifies HMAC-SHA256 signed JWTs. Access tokens carry
// identity (subject, username, roles) and are what Verify accepts; refresh
// tokens carry only the subject and a distinct audience so they can never be
// presented as access tokens. TokenService implements Verifier.
type TokenService struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// Option customizes a TokenService.
type Option func(*TokenService)

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(ts *TokenService) { ts.now = now }
}

// NewTokenService returns a TokenService signing with secret. accessTTL and
// refreshTTL set token lifetimes.
func NewTokenService(secret []byte, accessTTL, refreshTTL time.Duration, opts ...Option) *TokenService {
	ts := &TokenService{
		secret:     secret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(ts)
	}
	return ts
}

// AccessTTL is the configured access-token lifetime.
func (ts *TokenService) AccessTTL() time.Duration { return ts.accessTTL }

// tokenClaims is the JWT payload for both token kinds. Username and Roles are
// present only on access tokens.
type tokenClaims struct {
	jwt.RegisteredClaims
	Username string   `json:"username,omitempty"`
	Roles    []string `json:"roles,omitempty"`
}

// IssueAccess returns a signed access token for the account and its expiry time.
func (ts *TokenService) IssueAccess(accountID int64, username string, roles []string) (string, time.Time, error) {
	now := ts.now()
	exp := now.Add(ts.accessTTL)
	claims := tokenClaims{
		RegisteredClaims: ts.registered(accountID, accessAudience, now, exp),
		Username:         username,
		Roles:            roles,
	}
	signed, err := ts.sign(claims)
	return signed, exp, err
}

// IssueRefresh returns a signed refresh token for the account and its expiry.
func (ts *TokenService) IssueRefresh(accountID int64) (string, time.Time, error) {
	now := ts.now()
	exp := now.Add(ts.refreshTTL)
	claims := tokenClaims{RegisteredClaims: ts.registered(accountID, refreshAudience, now, exp)}
	signed, err := ts.sign(claims)
	return signed, exp, err
}

func (ts *TokenService) registered(accountID int64, audience string, now, exp time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer:    tokenIssuer,
		Subject:   strconv.FormatInt(accountID, 10),
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
}

func (ts *TokenService) sign(claims tokenClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(ts.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Verify parses and validates an access token and returns the application
// Claims. It enforces the HS256 method, the ecv4 issuer, the access-token
// audience (so a refresh token is rejected), and expiry. It implements Verifier.
func (ts *TokenService) Verify(raw string) (Claims, error) {
	var claims tokenClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return ts.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(accessAudience),
		jwt.WithTimeFunc(ts.now),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("verify token: %w", err)
	}

	accountID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Claims{}, fmt.Errorf("verify token: invalid subject %q: %w", claims.Subject, err)
	}

	var expiresAt time.Time
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time
	}
	return Claims{
		Subject:   claims.Subject,
		UserID:    accountID,
		Username:  claims.Username,
		Roles:     claims.Roles,
		ExpiresAt: expiresAt,
	}, nil
}
