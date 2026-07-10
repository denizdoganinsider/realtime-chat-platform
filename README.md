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

### Month 2 — Presence Service + persistence

New service: **presence-service** (`:8002`), backed by **Redis** (TTL-based
online/typing state — the first real Caching usage in this project, vs.
month 1 which touched no cache at all).

- `chat-service` gains a MySQL `messages` table (`room_id`, `user_id`,
  `content`, `created_at`) and a `GET /rooms/:room/messages` history
  endpoint (paginated, same pattern as notification-api's
  `notification_repository.go`). Broadcast stays in-memory; persistence is
  additive, not a replacement.
- On join/leave, `chat-service` **POSTs an event to presence-service**
  (`user_id`, `room`, `status`) — reusing the webhook-delivery pattern from
  notification-api (retry/backoff) instead of a new mechanism, since it's
  already-learned material applied to a new problem.
- Typing indicators are ephemeral WS messages (`type: "typing"`) — never
  persisted, never proxied through presence-service's HTTP API.
- `gateway` adds a third proxy target: `GET /presence/:room` → presence-service.
- **Verification**: two clients join a room, confirm `GET /presence/:room`
  shows both as online; one disconnects, confirm presence flips within the
  TTL window without an explicit "leave" message (crash/network-drop case).

### Month 3 — Load Balancer

Run **multiple chat-service instances** (`:8001`, `:8011`, ...) behind the
gateway. This is where the WebSocket **sticky-session problem** becomes
unavoidable: `chat-service`'s `Hub` is in-memory and per-process (see month
1's `hub.go` comment), so two clients in the same room MUST land on the
same instance or they can't see each other's messages.

- `gateway/internal/loadbalancer/`: a small pluggable LB with two
  strategies —
  - **round-robin** for stateless requests (`/register`, `/login`, `/rooms`
    listing — any instance can answer since presence-service, not
    chat-service, is the source of truth for room metadata by month 3).
  - **consistent hashing on the `room` query param** for `/ws` — the same
    room name always resolves to the same chat-service instance, which
    solves the sticky-session problem *without* needing shared state
    between instances (the trade-off explained explicitly: this is the
    "cheap" fix; the "real" fix — a Redis pub/sub backplane so any
    instance can serve any room — is called out as a stretch goal, not
    built, to keep the lesson about load balancing rather than distributed
    broadcast).
- Basic **health checking**: gateway polls each instance's `/health` on an
  interval and routes around a dead one; if the room's hashed instance is
  down, that room's connections fail closed (documented, not silently
  rerouted — rerouting would silently break the sticky guarantee).
- **Verification**: start 2 chat-service instances, confirm two clients in
  the same room always hit the same instance (log the `request_id` +
  instance port on both proxy and backend to prove it); kill one instance
  mid-session and confirm only the rooms hashed to it are affected.

### Month 4 — Notification Service + CDN

- **notification-service** (`:8003`): carries the webhook-delivery
  machinery over from notification-api almost directly — when a message
  arrives for a user who is offline (per presence-service), deliver a
  webhook/push event to that user's registered endpoint, with the same
  retry/exponential-backoff service already built once in month 1 of that
  project. This is the one component with the least new code — the point
  is recognizing an already-solved problem, not re-solving it.
- **CDN-style edge caching** for shared media (avatars, attachments): a
  small **media-service** (`:8004`) storing files on local disk, fronted by
  an in-memory "edge cache" layer in the gateway keyed by content hash —
  `ETag`, `Cache-Control: immutable`, and conditional `GET` (`304`) support.
  Not a real multi-region CDN (out of scope for a single machine), but the
  actual HTTP mechanics a CDN relies on: content-addressed caching,
  cache-hit/miss headers, and immutable-content invalidation-by-URL rather
  than invalidation-by-purge.
- **Verification**: upload an avatar, confirm the second `GET` for the same
  content hash returns `304` and a cache-hit log line at the gateway
  without a round-trip to media-service.

Each month's plan will be fleshed out into its own implementation plan
(scope, files, verification) right before that month starts, the same way
month 1 was — this section is the standing outline, not the final word.
