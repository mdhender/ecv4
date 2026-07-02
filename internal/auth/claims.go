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
}

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
