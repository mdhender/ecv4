# Curl examples

Run the skeleton server:

```bash
make run
```

Health check:

```bash
curl -s http://localhost:8080/healthz | jq .
```

Fetch the public contract:

```bash
curl -s http://localhost:8080/openapi.yaml
```

Example authenticated call after implementation:

```bash
TOKEN="replace-me"
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/games | jq .
```

Example order validation after implementation:

```bash
TOKEN="replace-me"
curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"factionId":7,"orders":"UNIT 101 MOVE N\nUNIT 102 SCOUT E\n"}' \
  http://localhost:8080/games/1/turns/1/orders:validate | jq .
```

Admin impersonation ("pose as" a player, for support/debugging). An admin mints
a short-lived (15-minute), non-refreshable token that bears a target non-admin
account's identity, then uses it like any bearer token — every response to a
request made with it carries an `Impersonated-Subject` header naming the target:

```bash
ADMIN_TOKEN="replace-me"          # a normal admin access token

# Mint an impersonation token for account 42.
IMP=$(curl -s \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"accountId":42}' \
  http://localhost:8080/admin/impersonation | jq -r .token)

# Act as account 42. -D - prints the response headers so you can see
# Impersonated-Subject: 42. The view is the target's, not the admin's.
curl -s -D - \
  -H "Authorization: Bearer $IMP" \
  http://localhost:8080/me | jq .
```

The minted token confers the target's access only (never admin), expires in 15
minutes, and cannot be refreshed. Impersonating yourself, another admin, or an
inactive account is rejected with 409.
