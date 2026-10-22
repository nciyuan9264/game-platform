#!/usr/bin/env bash
set -euo pipefail

: "${AUTH_DB_ADMIN_PASSWORD:=auth}"
: "${GAME_MIGRATOR_PASSWORD:=local-game-migrator}"
: "${GAME_RUNTIME_PASSWORD:=local-game-runtime}"
: "${AUTH_MIGRATOR_PASSWORD:=local-auth-migrator}"
: "${AUTH_RUNTIME_PASSWORD:=local-auth-runtime}"

psql --set=ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" \
  --dbname postgres \
  --set=auth_admin_password="$AUTH_DB_ADMIN_PASSWORD" \
  --set=game_migrator_password="$GAME_MIGRATOR_PASSWORD" \
  --set=game_runtime_password="$GAME_RUNTIME_PASSWORD" \
  --set=auth_migrator_password="$AUTH_MIGRATOR_PASSWORD" \
  --set=auth_runtime_password="$AUTH_RUNTIME_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE auth LOGIN PASSWORD %L', :'auth_admin_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auth')
\gexec
ALTER ROLE auth WITH LOGIN PASSWORD :'auth_admin_password';

SELECT format('CREATE ROLE game_migrator LOGIN PASSWORD %L', :'game_migrator_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'game_migrator')
\gexec
ALTER ROLE game_migrator WITH LOGIN PASSWORD :'game_migrator_password';

SELECT format('CREATE ROLE game_runtime LOGIN PASSWORD %L', :'game_runtime_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'game_runtime')
\gexec
ALTER ROLE game_runtime WITH LOGIN PASSWORD :'game_runtime_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;

SELECT format('CREATE ROLE auth_migrator LOGIN PASSWORD %L', :'auth_migrator_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auth_migrator')
\gexec
ALTER ROLE auth_migrator WITH LOGIN PASSWORD :'auth_migrator_password';

SELECT format('CREATE ROLE auth_runtime LOGIN PASSWORD %L', :'auth_runtime_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auth_runtime')
\gexec
ALTER ROLE auth_runtime WITH LOGIN PASSWORD :'auth_runtime_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;

SELECT 'CREATE DATABASE auth_center OWNER auth'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'auth_center')
\gexec

GRANT CONNECT, CREATE ON DATABASE game_platform TO game_migrator;
GRANT CONNECT ON DATABASE game_platform TO game_runtime;
GRANT CONNECT, CREATE ON DATABASE auth_center TO auth_migrator;
GRANT CONNECT ON DATABASE auth_center TO auth_runtime;

\connect game_platform
GRANT USAGE, CREATE ON SCHEMA public TO game_migrator;
GRANT USAGE ON SCHEMA public TO game_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE game_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO game_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE game_migrator IN SCHEMA public
  GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO game_runtime;

\connect auth_center
GRANT USAGE, CREATE ON SCHEMA public TO auth_migrator;
GRANT USAGE ON SCHEMA public TO auth_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE auth_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO auth_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE auth_migrator IN SCHEMA public
  GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO auth_runtime;
SQL
