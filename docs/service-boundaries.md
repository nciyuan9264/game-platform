# Service Boundaries

## Public Interfaces

PAM Gateway is the public HTTP owner. It exposes browser-facing Game Platform routes and adapts them to generated `GamePlatformService` RPC calls. Authentication Cookie behavior, HTTP status mapping and request/response compatibility belong there.

Realtime Gateway is the public WebSocket owner. Its external surface is limited to health checks and `/ws`.

Control Plane operational HTTP endpoints are private. RoomHost has no public HTTP surface.

```text
Browser --HTTP------> PAM Gateway ------RPC------> Platform RPC
Browser --WebSocket-> Realtime Gateway --NATS----> RoomHost
Platform RPC --------Control RPC-----------------> Control Plane
```

## Platform RPC

Platform RPC owns Game Platform business operations:

- create and list rooms;
- start another Match in an existing Room;
- list history and Match details;
- read materialized replay;
- read statistics and rankings;
- aggregate Game Platform data with Auth profiles.

Platform RPC uses `internal/authclient` for Auth identity/profile reads and `internal/controlclient` for room lifecycle operations. It does not read Auth tables and does not load game engines for online replay reads.

## Control Plane

Control Plane owns:

- active engine version resolution;
- compatible RoomHost selection;
- room and Match lifecycle control;
- owner initialization and node drain;
- instance, topology and runtime-event observation.

Control Plane does not own the public browser contract and does not route realtime player commands.

## Realtime Gateway

Realtime Gateway owns:

- WebSocket authentication;
- connection, session and presence state;
- routing commands to the current ready owner;
- RoomHost message delivery;
- reconnect backfill.

Gateway must not:

- execute game rules;
- choose an engine version;
- create or close rooms;
- trust client-supplied user or player identities;
- publish reserved Control Plane commands.

## RoomHost

RoomHost is an internal worker. It owns:

- RoomActor and MatchRuntime state;
- serial command execution;
- game engine and AI planner invocation;
- command log and snapshot persistence;
- reliable outbound messages and ACKs;
- history, replay and delayed spectator projections;
- recovery under a fenced owner epoch.

Each production binary contains one game definition. Shared runtime code depends on `internal/game` interfaces, not concrete game packages.

## Internal Transport

```text
Realtime Gateway -> room.cmd.<ownerNode> request/reply -> RoomHost
Control Plane ----> room.control.<ownerNode> request/reply -> RoomHost
RoomHost ---------> delivery subjects -------------------> Realtime Gateway
RoomHost ---------> JetStream cmdlog.<matchID> ----------> recovery/replay
```

Gateway credentials cannot publish `room.control.*`. Control Plane credentials cannot publish `room.cmd.*`.

Room is the long-lived lobby and routing boundary. Each Match has an independent ID, sequence, random seed, snapshot, command log, outbound cursor and spectator timeline. Commands from an older Match are rejected as stale.

## Auth Boundary

Auth Center owns credentials, token behavior and user profiles. Game Platform calls Auth RPC to:

- verify access tokens and derive user ID and roles;
- load current profiles;
- resolve display names for history and ranking views.

Game Platform never owns passwords, token issuance or Auth schema migrations. Authorization paths do not trust identity fields supplied in business requests.

## Data Boundary

| Data | Owner |
| --- | --- |
| Identity, credentials, profile and tokens | Auth Center |
| Catalog, room and Match metadata | Game Platform |
| Runtime snapshots, ACKs and reliable outbound | RoomHost runtime |
| History, replay and spectator projections | Game Platform |
| Node heartbeat, session and presence | Game Platform realtime runtime |
| Owner route and lease | Game Platform runtime |
| Per-Match command log | Game Platform runtime |

Legacy replay decoders and historical rule compatibility are retained for existing persisted data. They are not public runtime APIs.

## Game Log Boundary

Game logs use two channels:

| Channel | Writer | Reader | Purpose |
| --- | --- | --- | --- |
| Semantic operation log | RoomHost and game engine | Platform RPC, replay and admin tools | Ordered public/private events and replay timeline |
| State snapshot | RoomHost with engine encoding | recovery, replay seek and reconnect fallback | Fast state restoration |

Game engines emit semantic results through runtime interfaces. They do not write PostgreSQL, NATS or object storage directly.
