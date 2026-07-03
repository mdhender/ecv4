package auth

import (
	"crypto/rand"
	"encoding/hex"
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

// impersonationTTL is the fixed lifetime of an impersonation token. It is
// deliberately short and independent of the configurable access-token TTL: an
// admin acting as another account is a time-boxed support capability, not a
// session, so it is not tied to (and cannot be widened by) the access-token
// configuration.
const impersonationTTL = 15 * time.Minute

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

// RefreshTTL is the configured refresh-token lifetime.
func (ts *TokenService) RefreshTTL() time.Duration { return ts.refreshTTL }

// ImpersonationTTL is the fixed impersonation-token lifetime (15 minutes),
// exposed so handlers can report expiresInSeconds without duplicating the value.
func (ts *TokenService) ImpersonationTTL() time.Duration { return impersonationTTL }

// Now returns the service's current time from its (optionally injected) clock,
// so callers that need "now" for token-adjacent work — pruning expired refresh
// tokens, for instance — stay consistent with issuance and with WithClock in
// tests.
func (ts *TokenService) Now() time.Time { return ts.now() }

// tokenClaims is the JWT payload for both token kinds. Username and Roles are
// present only on access tokens; Family only on refresh tokens (the jti lives
// in RegisteredClaims.ID).
type tokenClaims struct {
	jwt.RegisteredClaims
	Username string   `json:"username,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Family   string   `json:"family,omitempty"`
	// Act is the impersonating admin's account id, present only on impersonation
	// tokens. Its presence is what marks a token as impersonation; an ordinary
	// access token omits it.
	Act int64 `json:"act,omitempty"`
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

// IssueImpersonation returns a signed, short-lived access token that lets an
// admin (actor) act as another account (accountID, with that account's username
// and roles). It uses the access audience, so every secured route accepts it
// like any access token and authorization keys off the impersonated account —
// but it carries the actor as an `act` claim (surfaced as Claims.Actor) for
// auditing, and its lifetime is the fixed impersonationTTL, not the configured
// access TTL. No refresh token accompanies it: it is deliberately
// non-refreshable (an access-audience token is rejected at /auth/refresh).
func (ts *TokenService) IssueImpersonation(accountID int64, username string, roles []string, actor int64) (string, time.Time, error) {
	now := ts.now()
	exp := now.Add(impersonationTTL)
	claims := tokenClaims{
		RegisteredClaims: ts.registered(accountID, accessAudience, now, exp),
		Username:         username,
		Roles:            roles,
		Act:              actor,
	}
	signed, err := ts.sign(claims)
	return signed, exp, err
}

// IssueRefresh returns a signed refresh token for the account and its expiry.
// jti is the token's unique id (its RegisteredClaims.ID) and family groups
// tokens rotated from one login; both are supplied by the caller (generated
// with NewTokenID) so they can be persisted for revocation and injected in
// tests.
func (ts *TokenService) IssueRefresh(accountID int64, jti, family string) (string, time.Time, error) {
	now := ts.now()
	exp := now.Add(ts.refreshTTL)
	registered := ts.registered(accountID, refreshAudience, now, exp)
	registered.ID = jti
	claims := tokenClaims{RegisteredClaims: registered, Family: family}
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

// NewTokenID returns a random 128-bit identifier as a 32-character hex string,
// used for refresh-token jti and family ids. The value is only an identifier,
// not a secret — the JWT signature is what makes a token unforgeable — so a
// collision-resistant random id is sufficient.
func NewTokenID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate token id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func (ts *TokenService) sign(claims tokenClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(ts.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// parse validates a signed token against the HS256 method, the ecv4 issuer, the
// given audience, and expiry, returning its raw claims. It is the shared body of
// Verify and VerifyRefresh; the audience is what keeps the two token kinds
// distinct (an access token cannot pass as a refresh token, or vice versa).
func (ts *TokenService) parse(raw, audience string) (tokenClaims, error) {
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
		jwt.WithAudience(audience),
		jwt.WithTimeFunc(ts.now),
	)
	return claims, err
}

// Verify parses and validates an access token and returns the application
// Claims. It enforces the HS256 method, the ecv4 issuer, the access-token
// audience (so a refresh token is rejected), and expiry. It implements Verifier.
func (ts *TokenService) Verify(raw string) (Claims, error) {
	claims, err := ts.parse(raw, accessAudience)
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
		Actor:     claims.Act,
	}, nil
}

// VerifyRefresh parses and validates a refresh token and returns its
// RefreshClaims. It mirrors Verify but enforces the refresh-token audience, so
// an access token is rejected. The returned jti and family let the caller look
// the token up for rotation and revocation.
func (ts *TokenService) VerifyRefresh(raw string) (RefreshClaims, error) {
	claims, err := ts.parse(raw, refreshAudience)
	if err != nil {
		return RefreshClaims{}, fmt.Errorf("verify refresh token: %w", err)
	}

	accountID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return RefreshClaims{}, fmt.Errorf("verify refresh token: invalid subject %q: %w", claims.Subject, err)
	}

	var expiresAt time.Time
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time
	}
	return RefreshClaims{
		Subject:   claims.Subject,
		UserID:    accountID,
		JTI:       claims.ID,
		Family:    claims.Family,
		ExpiresAt: expiresAt,
	}, nil
}
