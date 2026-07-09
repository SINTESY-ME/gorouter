#!/bin/sh
set -e

# When running in Docker Swarm, secrets are mounted as files at /run/secrets/.
# The gorouter binary expects env vars (not files), so we bridge here: read
# postgres_password and (optionally) key_secret from secrets, build the
# GOROUTER_DB_DSN string, and exec the binary.

if [ -f /run/secrets/postgres_password ]; then
  PW=$(cat /run/secrets/postgres_password)
  GOROUTER_DB_DSN="${GOROUTER_DB_DSN:-postgres://gorouter:${PW}@postgres:5432/gorouter?sslmode=disable}"
  export GOROUTER_DB_DSN
fi

if [ -f /run/secrets/key_secret ]; then
  export GOROUTER_KEY_SECRET=$(cat /run/secrets/key_secret)
fi

exec gorouter "$@"
