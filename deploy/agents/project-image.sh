#!/bin/bash
# Image Docker des agents d'exécution d'un projet (projects/<slug>/).
#   project-image.sh build  <slug>   construit agentic-os-agent-<slug>:<hash des locks> (+ :current)
#   project-image.sh check  <slug>   compare l'image :current aux fichiers lock de origin/<base>
#   project-image.sh verify <slug>   rejoue la vérification du projet dans l'image, sans réseau
#
# Le dépôt est récupéré dans $AOS_DATA_DIR/agent-images/<slug>/repo.git (bare, fetch de la base
# seulement). Le token GitHub d'Agentic OS est lu depuis son fichier par un credential helper : il
# n'apparaît ni dans l'argv, ni dans l'environnement, ni dans l'image.
set -euo pipefail

cmd="${1:-}"; slug="${2:-}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
dir="$root/projects/$slug"
[ -n "$cmd" ] && [ -n "$slug" ] && [ -f "$dir/project.env" ] || {
  echo "usage : $0 build|check|verify <slug>  (projects/<slug>/project.env requis)" >&2; exit 2; }
# shellcheck source=/dev/null
. "$dir/project.env"

data="${AOS_DATA_DIR:-/home/hermes/agentic-os-data}"
token_file="${AOS_GITHUB_TOKEN_FILE:-$data/secrets/github_token}"
work="$data/agent-images/$slug"
bare="$work/repo.git"
image="agentic-os-agent-$slug"
log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

fetch() {
  mkdir -p "$work"
  [ -d "$bare" ] || git init -q --bare "$bare"
  # shellcheck disable=SC2016 # expansions faites par git au moment de l'appel, pas ici
  local helper='!f() { test "$1" = get || exit 0; echo username=x-access-token; printf "password=%s\n" "$(cat "$AOS_TOKEN_FILE")"; }; f'
  AOS_TOKEN_FILE="$token_file" git --git-dir="$bare" -c credential.helper= -c "credential.helper=$helper" \
    fetch -q --depth 1 "https://github.com/$AOS_PROJECT_REPOSITORY.git" "+refs/heads/$AOS_PROJECT_BASE:refs/heads/$AOS_PROJECT_BASE"
  git --git-dir="$bare" rev-parse "refs/heads/$AOS_PROJECT_BASE"
}

lock_hash() { # $1 = commit
  local f out=""
  for f in $AOS_PROJECT_LOCKS; do
    out+="$f $(git --git-dir="$bare" rev-parse "$1:$f")"$'\n'
  done
  printf '%s' "$out" | sha256sum | cut -c1-12
}

export_tree() { # $1 = commit, $2 = destination
  mkdir -p "$2"
  git --git-dir="$bare" archive "$1" | tar -x -C "$2"
}

current_hash() {
  docker image inspect "$image:current" --format '{{ index .Config.Labels "io.agentic-os.lock-hash" }}' 2>/dev/null || true
}

case "$cmd" in
build)
  sha="$(fetch)"; hash="$(lock_hash "$sha")"
  log "$AOS_PROJECT_REPOSITORY@$AOS_PROJECT_BASE ${sha:0:7}, hash des locks $hash"
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  export_tree "$sha" "$tmp/src"
  log "construction de $image:$hash"
  DOCKER_BUILDKIT=1 docker build -q -f "$dir/Dockerfile" --build-context "project=$tmp/src" \
    --build-arg "AOS_LOCK_HASH=$hash" -t "$image:$hash" -t "$image:current" "$dir" >/dev/null
  log "OK : $image:$hash (alias $image:current), $(docker image inspect "$image:$hash" --format '{{.Size}}' | numfmt --to=iec)"
  ;;
check)
  sha="$(fetch)"; want="$(lock_hash "$sha")"; have="$(current_hash)"
  if [ -z "$have" ]; then log "aucune image $image:current : à construire"; exit 1; fi
  if [ "$have" = "$want" ]; then log "à jour ($want, $AOS_PROJECT_BASE ${sha:0:7})"; exit 0; fi
  log "À RECONSTRUIRE : image $have, $AOS_PROJECT_BASE ${sha:0:7} → $want"; exit 1
  ;;
verify)
  sha="$(git --git-dir="$bare" rev-parse "refs/heads/$AOS_PROJECT_BASE" 2>/dev/null || fetch)"
  [ -n "$(current_hash)" ] || { echo "aucune image $image:current : lancer build d'abord" >&2; exit 1; }
  tmp="$(mktemp -d)"; trap 'docker run --rm -v "$tmp:/w" alpine rm -rf /w/src >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT
  export_tree "$sha" "$tmp/src"
  log "vérification de $image:current sur $AOS_PROJECT_BASE ${sha:0:7}, sans réseau, UID $(id -u)"
  # Mêmes contraintes que les runs Hermes : --network none, UID de l'hôte, pas de secret.
  docker run --rm --network none --user "$(id -u):$(id -g)" -e HOME=/tmp \
    -v "$tmp/src:/w" -w /w "$image:current" bash -c \
    'set -e; if curl -sm5 https://repo.packagist.org >/dev/null 2>&1 || php -r "exit(@fsockopen(\"1.1.1.1\",443)?0:1);"; then echo "réseau ACCESSIBLE : refus" >&2; exit 3; fi; echo "réseau : coupé (attendu)"; '"$AOS_PROJECT_VERIFY"
  log "vérification OK"
  ;;
*) echo "commande inconnue : $cmd" >&2; exit 2 ;;
esac
