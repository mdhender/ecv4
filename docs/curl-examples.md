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
