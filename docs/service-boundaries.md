# Service Boundaries

## Public HTTP APIs

Public business HTTP APIs live in Platform API, not Gateway, RoomHost, or
Control Plane.

Gateway is the realtime ingress. It should expose only:

- `GET /healthz`
- `GET /ws`

RoomHost is an internal worker. It should expose no public HTTP surface. It
receives commands from internal transport, owns room actors, writes logs and
snapshots, and broadcasts through Gateway delivery subjects.

Platform API owns business HTTP APIs:

- create room
- start a new Match in an existing Room
- list rooms
- list matches/history
- fetch replay metadata
- fetch replay log pages
- fetch snapshot metadata
- create delayed spectator sessions
- fetch server-delayed spectator snapshots
- browser login/profile/avatar adaptation through Auth RPC
- business admin users and matches

Control Plane owns internal scheduling RPC and operational HTTP APIs:

- create/close/interruption/drain room commands
- instance state
- topology
- runtime events and service logs

Auth Center exposes Kitex RPC only. It owns identity data and token behavior;
Platform API owns the public browser contract and cookie policy.

## Internal RPC APIs

Auth Center exposes Kitex RPC for centralized user authentication:

- verify access token and return the lightweight `user_id/roles` principal
- refresh token after loading the current user role
- logout/revoke token
- get the complete user profile on demand
- get JWKS when a service still needs local verification

Game Platform should call Auth Center through `internal/authclient.Client`.
Platform API uses the generated Kitex client for browser Auth adaptation and
user directory aggregation. Gateway depends on the neutral verifier interface,
whose production implementation first verifies the principal and then calls
`GetProfile` only for WebSocket session fields such as name, email, and avatar.
Authorization-only paths do not perform that profile lookup. There is no local
JWT, JWKS, or mock fallback in production.

## Runtime APIs

Room lifecycle and Match commands are internal runtime messages:

```text
Gateway -> room.cmd.<ownerNode> request/reply -> RoomHost
Control Plane -> room.control.<ownerNode> request/reply -> RoomHost
RoomHost -> NATS pub/sub -> Gateway
RoomHost -> JetStream -> replay command log
```

Room remains the long-lived routing and lobby boundary. Each game round has a
separate `matchID`, sequence, seed, snapshot, command log, outbound cursor, and
spectator session. Delayed commands from an older Match are rejected with
`stale_match`.

These are not public HTTP APIs and should not be called by clients directly.
Gateway credentials cannot publish `room.control.*`, and Control Plane
credentials cannot publish `room.cmd.*`.

Live spectators do not subscribe to RoomHost or NATS directly. RoomHost writes
full read-only spectator views to PostgreSQL, whose own clock sets
`available_at` five minutes after capture. Platform API issues spectator
sessions only while a room is active and only to non-participants, then serves
snapshots whose `available_at` has passed.

## Game Logs

Game logs are split into two channels:

| Channel | Writer | Reader | Purpose |
| --- | --- | --- | --- |
| Operation log | RoomHost and game plugin | spectators, replay service, admin tools | Ordered semantic events, public/private visibility, replay timeline. |
| State snapshot | RoomHost and game plugin hints | recovery worker, replay seek, reconnect fallback | Fast restore and fast replay seek points. |

RoomHost must own persistence and delivery. Game plugins may emit semantic
events through `game.LogSink`, but they should not depend on Postgres, NATS,
or object storage directly.
