# Auth Center RPC Migration

## Goal

`auth/backend` is the shared RPC-only Auth Center. Browser-facing HTTP is owned
by Game Platform's Platform API, which delegates to Auth through generated
Kitex clients.

## Current State

- `auth/backend` owns the real user repository, email/code login, refresh,
  logout, profile, avatar upload, and RS256 JWKS.
- `../pam-idl/idl/auth/v1/service.thrift` is the only Auth service entrypoint.
- `auth/backend/cmd/auth-rpc` starts only the Kitex Auth service.
- `auth/backend/cmd/auth-rpc` can run a pure RPC Auth Center process.
- `game-platform/cmd/platform-api` exposes browser Auth APIs and calls Kitex.
- `game-platform/cmd/gateway` requires `AUTH_RPC_ADDR`.

## Target Flow

```text
Browser
  -> Platform API /platform/auth/*
  -> Kitex Auth RPC
  -> Platform API writes access_token / refresh_token cookies

Game Platform Gateway
  -> Auth Center Kitex VerifyToken
  -> receives lightweight user_id/roles principal
  -> propagates verified identity metadata
  -> Auth Center Kitex GetProfile
  -> receives complete UserIdentity
  -> establishes websocket session
```

## Phases

### Phase 1: Contract

- Done: keep `../pam-idl/idl/auth/v1/` as the sole source of truth.
- Done: generated Kitex server/client code in `auth/backend/kitex_gen`.
- Done: generated consumer client code in `game-platform/kitex_gen`.

### Phase 2: Auth Server

- Done: `auth/backend/cmd/auth-rpc` starts the pure RPC service.
- Done: `internal/rpc.AuthServiceHandler` adapts
  `internal/service.AuthService`.
- Done: registration, login, profile, avatar, user directory, and JWKS are
  available through RPC.
- Done: Auth Center has no HTTP listener.

### Phase 3: Game Platform Consumer

- Done: `game-platform/internal/authclient/kitex` implements
  `authclient.Client`.
- Done: Platform API owns `/platform/auth/*`, cookie policy, and platform user
  administration.
- Done: Platform API calls Auth RPC and never reads Auth tables.
- Done: Gateway requires `AUTH_RPC_ADDR` and always verifies through RPC.
- Done: principal-only paths use `VerifyPrincipal`; WebSocket connection uses
  `VerifyToken -> GetProfile` to load the profile explicitly.
- Done: no production JWKS/local mock fallback remains.

### Phase 4: Decommission Mock

- Done: remove `game-platform/cmd/auth` from runtime and compose.
- Unit tests use interface fakes; local smoke uses the real Auth RPC.

## RPC Error Codes

Use stable string codes in `AuthError.code`:

- `invalid_credentials`
- `missing_token`
- `invalid_token`
- `token_revoked`
- `token_expired`
- `user_not_found`
- `internal_error`

## Compatibility Notes

- Token subject should be normalized to string `userID` for game-platform.
- `VerifyToken` guarantees only `user_id` and `roles`; `display_name`, `email`,
  and `avatar` come from an explicit `GetProfile` call when the WebSocket
  session snapshot needs them.
- The verified `user_id/roles` must be propagated as RPC metadata before
  calling protected Auth methods such as `GetProfile`, `UpdateUser`, or
  `UploadAvatar`.
- RPC verification should be authoritative when enabled; Gateway should not
  silently accept a locally valid token if Auth Center says it is revoked.

## Local Verification

Run the regular distributed smoke with real Auth Center and Kitex RPC:

```bash
./scripts/run-local-smoke.sh
```

When the default ports are already occupied, reuse the existing infra and use
dedicated service ports:

```bash
REUSE_INFRA=1 USE_GATEWAY_LB=0 REDIS_DB=15 \
AUTH_RPC_ADDR=localhost:19190 \
PLATFORM_API_SERVER_ADDR=:19200 CONTROL_SERVER_ADDR=:19280 \
CONTROL_RPC_ADDR=localhost:19291 \
GATEWAY1_SERVER_ADDR=:19281 GATEWAY2_SERVER_ADDR=:19282 \
AUTH_HTTP_URL=http://localhost:19200 CONTROL_HTTP_URL=http://localhost:19200 \
GATEWAY1_HTTP_URL=http://localhost:19281 GATEWAY2_HTTP_URL=http://localhost:19282 \
GW1_WS=ws://localhost:19281/ws GW2_WS=ws://localhost:19282/ws \
./scripts/run-local-smoke.sh
```

Run the same path with RoomHost failover:

```bash
REUSE_INFRA=1 USE_GATEWAY_LB=0 REDIS_DB=15 \
AUTH_RPC_ADDR=localhost:19190 \
PLATFORM_API_SERVER_ADDR=:19200 CONTROL_SERVER_ADDR=:19280 \
CONTROL_RPC_ADDR=localhost:19291 \
GATEWAY1_SERVER_ADDR=:19281 GATEWAY2_SERVER_ADDR=:19282 \
AUTH_HTTP_URL=http://localhost:19200 CONTROL_HTTP_URL=http://localhost:19200 \
GATEWAY1_HTTP_URL=http://localhost:19281 GATEWAY2_HTTP_URL=http://localhost:19282 \
GW1_WS=ws://localhost:19281/ws GW2_WS=ws://localhost:19282/ws \
./scripts/run-recovery-smoke.sh
```
