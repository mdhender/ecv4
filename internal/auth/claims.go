package auth

import "time"

// Claims is the application-level identity extracted from a verified JWT.
// Replace this starter type with the claims package and signing strategy you
// choose for production.
type Claims struct {
	Subject   string
	UserID    int64
	Username  string
	Roles     []string
	ExpiresAt time.Time

	// Actor is set only on an impersonation token: the account id of the admin
	// who minted the token to act as UserID. Zero on an ordinary token.
	// Authorization always uses UserID (the effective identity); Actor is the
	// real admin behind it, carried for auditing and the Impersonated-Subject
	// indicator. See TokenService.IssueImpersonation.
	Actor int64
}

// Impersonated reports whether these claims come from an impersonation token —
// an admin (Actor) acting as the subject account (UserID).
func (c Claims) Impersonated() bool { return c.Actor != 0 }

// RefreshClaims is the identity extracted from a verified refresh token: the
// account plus the token's own id (jti) and its session family, which the
// refresh/logout handlers use to rotate and revoke tokens.
type RefreshClaims struct {
	Subject   string
	UserID    int64
	JTI       string
	Family    string
	ExpiresAt time.Time
}

func (c Claims) HasRole(role string) bool {
	for _, candidate := range c.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}
