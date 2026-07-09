#!/usr/bin/env bash
#
# deploy.sh — build the image, create Swarm secrets, and deploy the stack.
#
# Usage:
#   ./deploy.sh [stack-name]
#
# If secrets already exist, they are left untouched (Swarm secrets are
# immutable — re-creating them requires a stack update). To rotate a
# secret, delete it first with `docker secret rm <name>`.
set -euo pipefail

STACK_NAME="${1:-gorouter}"
SECRETS_DIR="${GOROUTER_SECRETS_DIR:-.secrets}"

log() { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[deploy]\033[0m %s\n' "$*" >&2; exit 1; }

# --- pre-flight -----------------------------------------------------------
command -v docker >/dev/null 2>&1 || err "docker not found in PATH"
docker info >/dev/null 2>&1 || err "docker daemon is not running or you lack permissions"

# Verify swarm mode. If not in swarm, initialize it on the current node.
if ! docker info --format '{{.Swarm.LocalNodeState}}' | grep -q '^active$'; then
  log "Swarm not active — initializing single-node swarm"
  docker swarm init >/dev/null
fi

# --- secrets --------------------------------------------------------------
mkdir -p "$SECRETS_DIR"
chmod 700 "$SECRETS_DIR"

ensure_secret() {
  local name="$1" file="$2"
  if docker secret inspect "$name" >/dev/null 2>&1; then
    log "secret $name already exists — keeping current value"
    return
  fi
  # Generate a random 32-char hex token if the file doesn't exist.
  if [ ! -s "$file" ]; then
    openssl rand -hex 16 > "$file"
    log "generated random secret → $file"
  fi
  docker secret create "$name" "$file" >/dev/null
  log "created secret $name"
}

ensure_secret postgres_password "$SECRETS_DIR/postgres_password"
ensure_secret key_secret          "$SECRETS_DIR/key_secret"

# --- build ----------------------------------------------------------------
log "building gorouter:latest"
docker build -t gorouter:latest .

# --- deploy ---------------------------------------------------------------
log "deploying stack $STACK_NAME"
docker stack deploy -c docker-stack.yml "$STACK_NAME"

log ""
log "Stack $STACK_NAME deployed. Services:"
log "  gorouter  → http://localhost:20128"
log "  postgres  → internal only (port 5432, reachable via gorouter-net)"
log ""
log "Secrets live in $SECRETS_DIR/ — keep this directory safe."
log "To tail logs:    docker service logs ${STACK_NAME}_gorouter"
log "To remove:       docker stack rm $STACK_NAME"
