# Auth package

This scaffold intentionally does not choose a JWT library or signing strategy.
Add one deliberately after deciding whether access tokens are signed with HS256,
RS256, EdDSA, or another algorithm.

Expected behavior:

- Login endpoints issue access and refresh tokens.
- Access tokens are sent as `Authorization: Bearer <token>`.
- Middleware verifies token signature, expiry, issuer/audience if used, and then
  adds `auth.Claims` to the request context.
- Object-level authorization remains in handlers/services, not in the OpenAPI
  generator. Examples: user can access game X, faction Y, turn Z.
