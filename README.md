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
| Load Balancer       | 3 |
| CDN                 | 4 (planned) |

## Architecture (Months 1-3)

Three independently deployable Go services, tied together for local development
via a Go workspace (`go.work`). From month 3 chat-service runs as **N
instances** behind the gateway:

```
                    ┌─────────────┐          ┌──────────────┐
  client  ─────────▶│   gateway    │ ───┬───▶│ chat-service  │──────┐
 (HTTP/WS)           │  :8000       │    │    │  :8001        │      │
                    │              │    │    └──────┬───────┘      │
                    │ - register    │    │           │              │ presence
                    │ - login       │    │    ┌──────┼───────┐      │  events +
                    │ - ws-ticket   │    └───▶│ chat-service  │─────┤  heartbeats
                    │ - reverse     │  load   │  :8011        │      │ (HTTP)
                    │   proxy       │ balancer└──────┼───────┘      ▼
                    │ - load        │                │       ┌──────────────────┐
                    │   balancer    │                │        │ presence-service  │
                    └──────┬───────┘                 │        │  :8002            │
                           │                        │        │ - /presence/:room │
                    ┌──────▼──────────────┐         │        │ - /rooms          │
                    │       MySQL          │◀───────┘        └────────┬─────────┘
                    │ chat_gateway_db      │                          │
                    │  (users)             │                   ┌──────▼──────┐
                    │ chat_service_db      │                   │    Redis     │
                    │  (messages)          │                   │ (presence)   │
                    └─────────────────────┘                   └─────────────┘
```

- **gateway** is the only public entry point. It owns authentication
  (`/register`, `/login`, JWT issuance), issues single-use WebSocket tickets,
  and acts as a **reverse proxy** in front of chat-service
  (`/rooms/:room/messages`, `/ws`) and presence-service (`/rooms`,
  `/presence/:room`). Go's stdlib `httputil.ReverseProxy` handles the
  WebSocket upgrade transparently — no bespoke proxy code was needed for that
  part, which is itself the lesson (see `gateway/internal/proxy/`). Since
  month 3 it is also the **load balancer** across chat-service instances
  (`gateway/internal/loadbalancer/`).
- **chat-service** owns real-time messaging: a hub/room/client pattern over
  `gorilla/websocket`, plus message persistence and paginated history. It does
  not issue tokens — it independently validates JWTs against a `JWT_SECRET`
  shared with the gateway via environment variable. This is the standard
  microservice pattern: share a **secret/config value**, not a code library,
  across independently deployable services. The Hub is in-memory and
  per-process, which is the whole reason month 3 exists.
- **presence-service** owns who is online, in Redis, with a TTL. chat-service
  instances tell it about joins and leaves over HTTP; it never reaches into
  chat-service, and chat-service never reaches into its Redis. Because it is
  the one component that sees every instance, it also answers `/rooms`.
- **Database per service.** The gateway owns `chat_gateway_db` (users);
  chat-service owns `chat_service_db` (messages). Same container, separate
  schemas, and `messages.user_id` has **no foreign key** to `users` — MySQL
  cannot enforce integrity across schemas owned by different deployables. The
  id is trusted because it came out of a signed JWT, not because the database
  checked it. That missing constraint is the price of the boundary, not an
  oversight.

### WebSocket auth: the ticket exchange

Month 1 put the JWT in the `/ws` query string, because browser WebSocket clients
cannot set an `Authorization` header on the handshake — and noted that URLs end
up in proxy and server access logs.

Month 2 removes it. The asymmetry that makes this work: a browser cannot set
headers on a handshake, but **the gateway can set them on the outbound proxy
request it makes**.

1. `POST /ws-ticket` with the Bearer token returns an opaque 30-second,
   single-use ticket (`gateway/internal/service/ticket_service.go`).
2. The client connects to `ws://…/ws?room=general&ticket=…`.
3. `WSTicketMiddleware` redeems the ticket (delete-on-read), mints a token,
   sets `Authorization` on the proxied request, and **strips the ticket from
   the URL** before anything downstream sees it.
4. chat-service reads the header, exactly like `/rooms` does.

So the long-lived token never appears in a URL, and the thing that does is worth
one connection for thirty seconds. The ticket store is an in-memory map with a
reaper goroutine: the gateway is a single process, and stays one in month 3,
where it *is* the load balancer rather than sitting behind one. If it is ever
replicated, that map is what moves to Redis.

The gateway's `LoggerMiddleware` still logs the request **path only**, and
chat-service still registers no request logger at all.

## Running locally

```bash
# 1. MySQL (two schemas) + Redis
docker-compose up -d

# 2. presence-service
cd presence-service && JWT_SECRET=my-secret-key PRESENCE_API_KEY=dev-presence-key \
  go run ./cmd/presence                                              # :8002

# 3. chat-service - two instances (month 3); one is enough for months 1-2
cd chat-service && JWT_SECRET=my-secret-key PRESENCE_API_KEY=dev-presence-key \
  SERVER_PORT=8001 go run ./cmd/chat                                 # :8001
cd chat-service && JWT_SECRET=my-secret-key PRESENCE_API_KEY=dev-presence-key \
  SERVER_PORT=8011 go run ./cmd/chat                                 # :8011

# 4. gateway, told about both
cd gateway && JWT_SECRET=my-secret-key \
  CHAT_SERVICE_URLS=http://localhost:8001,http://localhost:8011 \
  go run ./cmd/gateway                                               # :8000
```

`JWT_SECRET` and `PRESENCE_API_KEY` have **no defaults** — every service that
needs one fails fast at startup if it is unset, on purpose: a predictable
default would let anyone forge tokens or impersonate chat-service. `JWT_SECRET`
must match across all three (shared secret, not shared code — see Architecture);
`PRESENCE_API_KEY` must match between chat-service and presence-service.

### Configuration

| Service | Variable | Default |
|---|---|---|
| gateway | `CHAT_SERVICE_URLS` | `http://localhost:8001` (comma-separated) |
| gateway | `LB_HEALTH_INTERVAL_SECONDS` | `5` |
| gateway | `PRESENCE_SERVICE_URL` | `http://localhost:8002` |
| chat-service | `SERVER_PORT` | `8001` |
| chat-service | `INSTANCE_ID` | `chat-<SERVER_PORT>` |
| chat-service | `DB_NAME` | `chat_service_db` |
| chat-service | `PRESENCE_SERVICE_URL` | `http://localhost:8002` |
| chat-service | `PRESENCE_API_KEY` | *required* |
| chat-service | `PRESENCE_HEARTBEAT_SECONDS` | `30` (keep below TTL/2) |
| chat-service | `WS_ALLOWED_ORIGINS` | `http://localhost:3000,http://localhost:8000` |
| presence-service | `REDIS_ADDR` | `localhost:6380` |
| presence-service | `PRESENCE_TTL_SECONDS` | `90` |

`WS_ALLOWED_ORIGINS` closes month 1's other gap: `upgrader.CheckOrigin` used to
return `true` unconditionally. A **missing** `Origin` header is still allowed —
cross-site WebSocket hijacking is a browser attack, browsers always send
`Origin` and cannot be told not to, so rejecting header-less native clients
would buy no security at all.

### Applying the schema to an existing volume

`docker-entrypoint-initdb.d` scripts run **only when the data directory is
empty**, and `mysql_data` is a named volume — so adding `db/chat_schema.sql`
does nothing to a database that already exists. Either:

```bash
# non-destructive (keeps registered users)
docker exec -i chat_gateway_mysql mysql -uroot -proot < db/chat_schema.sql

# or full reset
docker-compose down -v && docker-compose up -d
```

The non-destructive path only works because that file carries its own
`CREATE DATABASE IF NOT EXISTS` / `USE`, rather than relying on `MYSQL_DATABASE`.

### Smoke test

```bash
JSON='Content-Type: application/json'

curl -s -X POST localhost:8000/register -H "$JSON" \
  -d '{"email":"a@test.com","password":"Passw0rd1"}'
TOKEN=$(curl -s -X POST localhost:8000/login -H "$JSON" \
  -d '{"email":"a@test.com","password":"Passw0rd1"}' | jq -r .token)

# one ticket per connection - it is single-use
TICKET=$(curl -s -X POST localhost:8000/ws-ticket -H "Authorization: Bearer $TOKEN" | jq -r .ticket)
websocat -v "ws://localhost:8000/ws?room=general&ticket=$TICKET"
```

Open two sessions in the same room with their own tickets and confirm messages
sent from one appear in the other — proof that proxy + microservice + WebSocket
are working together end to end. With two chat-service instances running, that
is also proof the load balancer kept both sessions on the same one: `-v` shows
the handshake headers, and `X-Chat-Instance` names the instance that answered.

```bash
go test ./gateway/... ./chat-service/... ./presence-service/... -race
```

The presence repository tests need Redis on `localhost:6380` and skip when it is
not running; everything else runs anywhere.

## Month 2 — Presence + persistence

### Message persistence

`chat-service` stores chat messages in `chat_service_db.messages` and serves
`GET /rooms/:room/messages?page=&per_page=` with the same limit/offset shape as
notification-api's `notification_repository.go`. **Broadcast stays in-memory** —
persistence is additive, not a replacement.

Two deliberate departures from the predecessor:

- `created_at` is `TIMESTAMP(3)` and queries order by `created_at DESC, id DESC`.
  Second-resolution ties make `LIMIT/OFFSET` non-deterministic — rows can repeat
  or vanish between pages. The `id` tiebreak is free: InnoDB appends the primary
  key to every secondary index, so `idx_messages_room_created` already sorts by
  it.
- The stored timestamp is the one the room stamped on the **broadcast**, passed
  down through the archive queue, so history and what clients actually saw agree
  exactly.

### Presence

Redis holds one sorted set per room (month 3 widened the member to
`<user_id>:<instance_id>` — see there):

```
presence:room:<room>   member = "<user_id>"   score = expiry epoch millis
```

Redis has no per-member TTL, so expiry is **logical**: writes stamp an expiry
score and the read path prunes anything already past it (both in one
`MULTI`/`EXEC`, so a concurrent heartbeat cannot slip between them). A key-level
`EXPIRE` refreshed on every write reclaims rooms nobody reads again. The
trade-off worth naming: expiry became *our* job on read instead of Redis's, in
exchange for an O(log N + M) read instead of a `SCAN` over the keyspace.

`presence-service` runs two auth schemes side by side, which is the point:
`POST /events` and `POST /heartbeat` are service-to-service and take a shared
`X-Presence-Key` (compared in constant time); `GET /presence/:room` is the
end-user read path and takes the same Bearer token as `/me`. Only the read path
is proxied by the gateway.

### Why nothing blocks the room

`Room.run()` is a single goroutine serving every broadcast in its room. An
inline database write or presence POST — up to seconds with retries — would
stall every client in that room. So the room depends on two interfaces
(`PresenceNotifier`, `MessageArchiver`) with **no error return and no blocking
call**, implemented by `internal/dispatch`: bounded queues, one worker each,
non-blocking enqueue that drops and logs when full. It is a structural
guarantee, not a convention — there is no way to write a blocking call there.

The presence worker is deliberately not scalable past one: the queue is the only
thing keeping a user's `online` and `offline` in order.

### Why dropping a presence event is safe

A sweeper goroutine walks the hub every `PRESENCE_HEARTBEAT_SECONDS` and sends
one bulk heartbeat per room. That makes presence **self-healing**: a dropped
event, a presence-service restart, or a user whose second socket closed all
repair themselves on the next sweep. Heartbeats are the one call that is *not*
retried — the next sweep is the retry.

The interval and TTL follow a 3× rule (30s / 90s by default) so two consecutive
misses are tolerated.

Presence is also cleared on paths that emit no leave frame:

- **slow-consumer eviction** — an evicted client never reaches the unregister
  case, so `broadcastEvent` reports it offline itself. It deliberately does not
  re-broadcast a `leave` frame: `broadcastEvent` calling itself can evict again
  and the room mutex is not reentrant. The frame is cosmetic; the presence flip
  is not.
- **graceful shutdown** — chat-service marks everyone offline before exiting.

### Typing indicators

`{"type":"typing"}` is fanned out in-room and forgotten: never stored, never
sent to presence-service, never in Redis. A near-keystroke-rate event has no
business on an HTTP hop or in a datastore; receivers clear the indicator on
their own timer. (The month-2 outline originally described presence-service as
holding "online/typing state" — that contradicted the same paragraph's rule
about not proxying typing, and this is the resolution.)

### Verification

```bash
# both users online
curl -s localhost:8000/presence/general -H "Authorization: Bearer $TOKEN" | jq
# => {"room":"general","users":[1,2],"count":2}
docker exec chat_redis redis-cli ZRANGE presence:room:general 0 -1 WITHSCORES

# history is newest-first, and typing never lands in it
curl -s "localhost:8000/rooms/general/messages?page=1&per_page=10" \
  -H "Authorization: Bearer $TOKEN" | jq
```

The three disconnect cases are genuinely different mechanisms, and only one of
them is a TTL test:

| Case | What clears presence | How long |
|---|---|---|
| Client closes the tab | explicit `offline` on unregister | sub-second |
| Client goes silent (`kill -STOP`, network drop) | `pongWait` closes the socket, then explicit `offline` | ≤ 60s |
| **chat-service itself dies (`kill -9`)** | **nothing can send an offline — score expiry alone** | ≤ `PRESENCE_TTL_SECONDS` |

Only the third is the "no explicit leave" case. `kill -9` on the *client* is
not one: the OS sends a RST, the read pump errors immediately, and an explicit
leave goes out.

```bash
# run with PRESENCE_TTL_SECONDS=15 PRESENCE_HEARTBEAT_SECONDS=5 for a fast demo
kill -9 $(pgrep -f 'exe/chat$')
for i in $(seq 1 8); do
  printf '%s count=' "$(date +%T)"
  curl -s localhost:8000/presence/general -H "Authorization: Bearer $TOKEN" | jq -c .count
  sleep 3
done
# => 2 2 2 2 0 0 0 0
```

Self-healing, and proof the room never stalls:

```bash
# stop presence-service: chat-service logs delivery failures, messages keep
# flowing instantly, Redis empties once the TTL elapses
# start it again: both users are back within one heartbeat interval, with no
# client having reconnected
```

## Month 3 — Load Balancer

Two chat-service instances (`:8001`, `:8011`) behind the gateway. This is
where the WebSocket **sticky-session problem** stops being a comment in
`hub.go` and becomes a bug you can watch happen: the `Hub` is in-memory and
per-process, so two clients in the same room MUST land on the same instance or
they never see each other's messages.

### Two strategies, because there are two kinds of request

`gateway/internal/loadbalancer/` has a `Strategy` interface with one method,
`Pick(request) (backend, error)`, and two implementations. Which one a route
uses is decided in `main.go`, per route, because the routes are not the same
problem:

| Route | Strategy | Why |
|---|---|---|
| `GET /rooms/:room/messages` | **round-robin** | A database read. Any instance can answer, so spread the load. |
| `GET /ws` | **consistent hash on `?room=`** | The room lives in one instance's memory. Every connection to it must go there. |

Round-robin counts over the *healthy* list, not the full one: skipping a dead
instance by re-picking would hand the instance after it double the traffic.

Consistent hashing is a ring of 150 virtual points per instance, keyed by
FNV-1a with a finalizer mix. Both details matter. With one point per instance,
two instances could split the ring 90/10. And the virtual node labels differ
only in a numeric suffix (`a:1#0`, `a:1#1`, ...) — raw FNV-1a of near-identical
inputs gives near-identical high bits, and the first version of this ring
measured a **70/20/10** split across three instances before the MurmurHash3
finalizer was added. `TestConsistentHashSpreadsRoomsEvenly` is the regression
test for that. The other property the ring buys over `hash % N` is that adding
a fourth instance moves about a quarter of the rooms rather than three
quarters; `TestConsistentHashMovesFewKeysWhenScalingOut` pins it.

`?room=` omitted hashes as `general`, exactly as chat-service defaults it,
so the two ways of naming the default room cannot split across instances.

### Health checks, and why a dead instance's rooms fail closed

The gateway polls every instance's `/health` every `LB_HEALTH_INTERVAL_SECONDS`
(one synchronous sweep at startup, so the very first request already sees the
truth). `GET /health/backends` — behind the Bearer token, because internal
host:port pairs and which of them are degraded is topology an anonymous caller
on the public entry point has no business with — reports what it knows:

```json
{"chat_service":[{"backend":"localhost:8001","healthy":true},
                 {"backend":"localhost:8011","healthy":false}]}
```

Round-robin routes around a dead instance. Consistent hash **does not**. The
ring is built over all configured instances and never rebuilt, so a room whose
instance is down answers `503` until the instance returns. That is the
trade-off named up front in the roadmap, and it is the right one: if the ring
shrank when an instance died, its rooms would move to a neighbour, and when it
came back they would move again — clients that connected in between would be
split across two instances of the same room, the exact bug the strategy exists
to prevent. Failing closed keeps the guarantee; the cost is that those rooms
are unavailable for the outage. The "real" fix — a Redis pub/sub backplane so
any instance can serve any room — is a different lesson (distributed
broadcast, not load balancing) and is deliberately not built.

### What moved: `/rooms` is now presence-service's

With N instances, each chat-service only knows the rooms its own Hub holds, so
a round-robined `/rooms` would answer differently on every call. Redis is the
one place that sees every instance, so `GET /rooms` is now served by
presence-service and proxied by the gateway. chat-service keeps its own
`/rooms`, reachable on its port directly and not through the gateway — that
per-instance view is how the verification below proves stickiness.

The body changed with the move, and the change is semantic, not cosmetic:

```
chat-service     GET :8001/rooms   [{"name":"general","clients":2}]   sockets on this instance
presence-service GET :8000/rooms   [{"name":"general","count":2}]     distinct users, all instances
```

`clients` counts connections — one user with two tabs is two — and only the
ones this process holds. `count` is distinct users fleet-wide, the same number
`/presence/:room` reports. A client that read `clients` needs to read `count`.

To make it cheap, presence writes maintain a `presence:rooms` index ZSET as a
by-product (same expiry-score scheme as the rooms themselves), rather than the
read path doing a `SCAN` over the keyspace. The last `offline` out of a room
drops it from the index — "is the room empty" and "remove it from the index"
run as one Lua script, because as two round trips a join on another instance
could land between them and have its index entry deleted out from under it. A
`kill -9`'d instance's rooms fall out by score.

### The presence flaw month 2 named, fixed

The month 2 roadmap called it out: with N instances, an `offline` from
instance A would clobber an `online` from instance B for the same user. The
member is now `<user_id>:<instance_id>`:

```
presence:room:<room>   member = "<user_id>:<instance_id>"   score = expiry epoch millis
presence:rooms         member = "<room>"                    score = expiry epoch millis
```

A's `ZREM` touches only A's entry, each instance's heartbeat re-stamps only
its own members, and the read path collapses the instances back to one user
id. `INSTANCE_ID` defaults to `chat-<port>` (two instances on one machine
differ only by port); a real deployment sets a hostname. The rule — up to 64
of `[A-Za-z0-9_.-]`, no colons, since it sits after one in the member — is
enforced twice: chat-service refuses to start on a bad id, and presence-service
rejects one on the wire with `400`. The first check exists because of the
second: chat-service treats a `400` as final and does not retry, so without a
startup check an instance with a bad id would run for its whole life with
presence silently broken. The event and heartbeat bodies both carry the id.
This is the second wire-contract change between the two services, and, as with
the first, they change together and share no code.

The read path still accepts month 2's bare `<user_id>` members. An in-place
upgrade against the same Redis holds them for up to one TTL, and answering
`500` for that window would be a self-inflicted outage.

### Verification

Which instance answered is visible three ways, all deliberately: the
`X-Chat-Instance` response header (on the WebSocket 101 too — gorilla writes
that response itself, so the header is passed to `Upgrade` rather than set on
the `ResponseWriter`), the gateway's `proxying request` log line, and
chat-service's one `websocket connected` line per connection. The last two
share a `request_id`.

```bash
# the same room lands on the same instance every time; different rooms spread
for room in general random alpha beta; do
  printf '%-8s' $room
  for i in 1 2 3; do
    T=$(curl -s -X POST localhost:8000/ws-ticket -H "Authorization: Bearer $TOKEN" | jq -r .ticket)
    websocat -v "ws://localhost:8000/ws?room=$room&ticket=$T" 2>&1 </dev/null | grep -o 'X-Chat-Instance: [^ ]*' | tr '\n' ' '
  done; echo
done
# => general  chat-8001 chat-8001 chat-8001
#    random   chat-8011 chat-8011 chat-8011

# history round-robins regardless of room
for i in 1 2 3 4; do
  curl -si "localhost:8000/rooms/general/messages?per_page=1" \
    -H "Authorization: Bearer $TOKEN" | grep X-Chat-Instance
done
# => chat-8001 chat-8011 chat-8001 chat-8011

# the fleet-wide view versus each instance's own (connect a client to general
# and one to random first)
curl -s localhost:8000/rooms -H "Authorization: Bearer $TOKEN"   # both rooms
curl -s localhost:8001/rooms -H "Authorization: Bearer $TOKEN"   # only general
curl -s localhost:8011/rooms -H "Authorization: Bearer $TOKEN"   # only random

# the same request_id on both sides of the proxy
grep '"path":"/ws"' gateway.log | jq -c '{backend,request_id}'
grep 'websocket connected' chat8001.log | jq -c '{instance,room,request_id}'
```

Kill one instance mid-session:

```bash
kill -9 $(lsof -ti :8011)
sleep $LB_HEALTH_INTERVAL_SECONDS
curl -s localhost:8000/health/backends -H "Authorization: Bearer $TOKEN" \
  | jq -c .chat_service                                  # 8011 healthy:false
# a room hashed to 8011 => 503 "the instance serving this room is unavailable"
# a room hashed to 8001 => connects as before
# history => 200 every time, all from chat-8001
```

Start it again and its rooms are reachable within one health interval, on the
same instance they were on before — nothing moved.

## Roadmap (not yet implemented)

### Month 4 — Notification Service + CDN

- **notification-service** (`:8003`): carries the webhook-delivery
  machinery over from notification-api almost directly — when a message
  arrives for a user who is offline (per presence-service), deliver a
  webhook/push event to that user's registered endpoint, with the same
  retry/exponential-backoff service already built once in that project.
  This is the one component with the least new code — the point is
  recognizing an already-solved problem, not re-solving it. HMAC-signed
  bodies belong here too, which is why month 2's service-to-service calls
  settled for a shared API key over localhost.
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
months 1 to 3 were — this section is the standing outline, not the final word.

## Known trade-offs

- **Three copies of `jwt.go`.** Deliberate: a shared `internal/auth` module
  would couple three independently deployable services at build time, which is
  exactly what "share a config value, not a code library" rules out. Worth
  naming the cost honestly though — three copies is roughly where the discipline
  stops being free, and a fourth service is the moment to re-evaluate rather
  than copy again.
- **Presence is eventually consistent by design.** Events can be dropped when a
  queue is full; the heartbeat is the source of truth. Exact presence would need
  a different design, and a chat sidebar does not need one.
- **Same user, two sockets in one room *on the same instance*** briefly shows
  offline when one closes, until the next heartbeat. Month 3 fixed the
  cross-instance version of this (per-instance members); within one instance,
  refcounting in Redis is the proper fix and the sweep covers it for now.
- **A dead instance takes its rooms with it** until it is back. Failing closed
  is the honest cost of sticky routing without a backplane (see month 3).
- **Instance membership is static.** `CHAT_SERVICE_URLS` is read at startup;
  adding an instance means restarting the gateway. Service discovery is a
  different lesson.
- **Health is polled, not observed.** A request that hits an instance in the
  seconds between its death and the next poll gets a `502`. Marking an
  instance down on a proxy error would close that gap, but a client hanging up
  mid-WebSocket also surfaces as a proxy error, and telling the two apart
  reliably was not worth the risk of flapping a healthy instance.
- **No rate limiting on typing frames.** notification-api's Redis rate limiter
  is the obvious port, but it would re-learn month-1 material.
