# Realtime Chat Platform

Learning project: backend system design concepts through a real, running system,
one topic-cluster per month. Predecessor project: `notification-api`
(Rate Limiting, Auth & Authorization, Redis Caching, Webhooks).

Remaining concepts from the source material and the month they're addressed:

| Concept          | Month |
|-------------------|-------|
| Microservice       | 1 |
| Reverse Proxy       | 1 |
| WebSocket           | 1 |
| Load Balancer       | 3 (planned) |
| CDN                 | 4 (planned) |

## Architecture (Month 1)

Two independently deployable Go services, tied together for local development
via a Go workspace (`go.work`):

```
                    ┌─────────────┐        ┌──────────────┐
  client  ─────────▶│   gateway    │──────▶│ chat-service  │
 (HTTP/WS)           │  :8000       │ proxy  │  :8001        │
                    │              │        │               │
                    │ - register    │        │ - /ws (hub)   │
                    │ - login       │        │ - /rooms      │
                    │ - reverse     │        └──────────────┘
                    │   proxy       │
                    └──────┬───────┘
                           │
                       ┌───▼───┐
                       │ MySQL  │  (users only)
                       └───────┘
```

- **gateway** is the only public entry point. It owns authentication
  (`/register`, `/login`, JWT issuance) and acts as a **reverse proxy** in
  front of chat-service for `/rooms` and `/ws`. Go's stdlib
  `httputil.ReverseProxy` handles the WebSocket upgrade transparently — no
  bespoke proxy code was needed for that part, which is itself the lesson
  (see `gateway/internal/proxy/reverse_proxy.go`).
- **chat-service** owns real-time messaging: a hub/room/client pattern over
  `gorilla/websocket`. It does not talk to MySQL and does not issue tokens —
  it independently validates JWTs against a `JWT_SECRET` shared with the
  gateway via environment variable. This is the standard microservice
  pattern: share a **secret/config value**, not a code library, across
  independently deployable services.
- Message history is **not persisted** in month 1 — broadcast is in-memory
  only. Persistence + presence land in month 2.
- `/rooms` requires the same Bearer token as `/me`; the gateway enforces it
  and chat-service independently re-validates it, matching the "each
  service checks its own auth" theme used for `/ws`.

### Known limitation: JWT over the WebSocket URL

`/ws?token=...` carries the JWT as a query param because browser WebSocket
clients can't set an `Authorization` header on the handshake. This is a
real trade-off, not an oversight: URLs can end up in proxy/server access
logs. The gateway's `LoggerMiddleware` (`gateway/internal/middleware/logger.go`)
deliberately logs the request **path only**, never the raw URL/query
string; chat-service registers no request logger at all, so neither
service writes the token anywhere. A more complete fix — a short-lived,
single-use ticket exchanged for the WS connection instead of the
long-lived JWT itself — is left for a later month.

## Running locally

```bash
# 1. MySQL (gateway's user store only)
docker-compose up -d

# 2. chat-service
cd chat-service && JWT_SECRET=my-secret-key go run ./cmd/chat        # :8001

# 3. gateway (separate terminal)
cd gateway && JWT_SECRET=my-secret-key go run ./cmd/gateway          # :8000
```

`JWT_SECRET` has **no default** — both services fail fast at startup if it's
unset, on purpose: a predictable default would let anyone forge valid tokens
for any user. It must be set to the *same* value for both services (shared
secret, not shared code — see Architecture above).

### Smoke test

```bash
# register + login via the gateway
curl -s -X POST localhost:8000/register -d '{"email":"a@test.com","password":"Passw0rd1"}'
TOKEN=$(curl -s -X POST localhost:8000/login -d '{"email":"a@test.com","password":"Passw0rd1"}' | jq -r .token)

# two clients through the gateway's proxy, not chat-service directly
websocat "ws://localhost:8000/ws?room=general&token=$TOKEN"
```

Open two `websocat` sessions with the same token/room and confirm messages
sent from one appear in the other — proof that proxy + microservice +
WebSocket are working together end to end.

## Roadmap (not yet implemented)

- **Month 2**: Presence Service (online/typing), message persistence + history.
- **Month 3**: Load Balancer — multiple chat-service instances behind the
  gateway, round-robin/least-conn, and the WebSocket sticky-session problem.
- **Month 4**: Notification Service (webhooks, carried over from
  notification-api), CDN-style edge caching for shared media.
