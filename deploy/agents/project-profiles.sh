#!/bin/bash
# Crée ou met à jour les profils Hermes propres à un projet : aos-<slug>-developer|reviewer|qa.
#   project-profiles.sh <slug>
#
# - Clonés des profils par défaut aos-<rôle> (SOUL, skills, outils, isolation Docker sans réseau),
#   mémoire vidée, clé API propre (secrets/hermes/<profil>.key, jamais affichée).
# - Image : agentic-os-agent-<slug>:current (task project:image:build).
# - Montage : seulement workspaces/<slug> (lecture-écriture pour le développeur, lecture seule sinon).
# - Un profil existant est sauvegardé (config.yaml, .env) avant modification.
# Les profils dev-* et aos-* par défaut ne sont jamais modifiés.
set -euo pipefail
umask 077

slug="${1:-}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
[ -n "$slug" ] && [ -f "$root/projects/$slug/project.env" ] || { echo "usage : $0 <slug>" >&2; exit 2; }
# shellcheck source=/dev/null
. "$root/projects/$slug/project.env"

hermes_bin="${HERMES_BIN:-hermes}"
hermes_home="${HERMES_HOME:-$HOME/.hermes}"
data="${AOS_DATA_DIR:-/home/hermes/agentic-os-data}"
ws="$data/workspaces/$slug"
image="agentic-os-agent-$slug:current"
backup="$hermes_home/backups/aos-$slug-profiles-$(date -u +%Y%m%dT%H%M%SZ)"
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

docker image inspect "$image" >/dev/null 2>&1 || { echo "image $image absente : task project:image:build PROJECT=$slug" >&2; exit 1; }
# Créé ici avec l'UID de l'hôte : sinon Docker le créerait en root au premier montage.
mkdir -p "$ws"; chmod 755 "$ws"
# Dépôt des enregistrements du QA navigateur (aos-qa-browser), seul dossier inscriptible du QA ;
# Agentic OS en copie le contenu validé dans son stockage (recordings/store, jamais monté ici).
rec="$data/recordings/inbox/$slug"
mkdir -p "$rec"; chmod 755 "$rec"
mkdir -p "$data/secrets/hermes"

for role in developer reviewer qa; do
  p="aos-$slug-$role"; dir="$hermes_home/profiles/$p"
  if [ -d "$dir" ]; then
    mkdir -p "$backup/$p"; cp -p "$dir/config.yaml" "$dir/.env" "$backup/$p/"
    log "$p : existe (sauvegarde $backup/$p)"
  else
    "$hermes_bin" profile create "$p" --clone-from "aos-$role" --no-alias \
      --description "Agentic OS — $role du projet $slug" >/dev/null
    # Pas de mémoire héritée ; une clé API propre est générée plus bas.
    : > "$dir/memories/MEMORY.md" 2>/dev/null || true
    : > "$dir/memories/USER.md" 2>/dev/null || true
    sed -i '/^API_SERVER_KEY=/d' "$dir/.env"
    log "$p : créé depuis aos-$role"
  fi

  mode=""; [ "$role" = developer ] || mode=":ro"
  "$hermes_bin" -p "$p" config set terminal.docker_image "$image" >/dev/null
  vols="[\"$ws:$ws$mode\"]"
  [ "$role" = qa ] && vols="[\"$ws:$ws$mode\", \"$rec:$rec\"]"
  "$hermes_bin" -p "$p" config set terminal.docker_volumes "$vols" >/dev/null
  "$hermes_bin" -p "$p" config set terminal.container_cpu "$AOS_PROJECT_CPU" >/dev/null
  "$hermes_bin" -p "$p" config set terminal.container_memory "$AOS_PROJECT_MEMORY_MB" >/dev/null
  "$hermes_bin" -p "$p" config set terminal.timeout "$AOS_PROJECT_COMMAND_TIMEOUT" >/dev/null
  sed -i '/^TERMINAL_TIMEOUT=/d' "$dir/.env"

  # Clé API du profil : générée si absente, puis copiée pour Agentic OS (valeur jamais affichée).
  if ! grep -q '^API_SERVER_KEY=.\{32,\}' "$dir/.env"; then
    sed -i '/^API_SERVER_KEY=/d' "$dir/.env"
    printf 'API_SERVER_KEY=%s\n' "$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')" >> "$dir/.env"
  fi
  sed -n 's/^API_SERVER_KEY=//p' "$dir/.env" | tail -n1 > "$data/secrets/hermes/$p.key"
  chmod 600 "$data/secrets/hermes/$p.key"
  log "$p : image $image, montage $ws$mode, clé secrets/hermes/$p.key"
done
log "profils prêts. Projet Agentic OS : agent_profiles = {developer: aos-$slug-developer, reviewer: aos-$slug-reviewer, qa: aos-$slug-qa}"
