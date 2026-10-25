#!/usr/bin/env bash
# Mise à jour d'Agentic OS vers une version (tag d'image), avec sauvegarde préalable obligatoire.
#
# Usage : deploy/ops/upgrade.sh <tag>          (ex. 1.0.1 ; le dépôt doit être sur ce tag)
#
# 1. sauvegarde pre-upgrade (refus de continuer si elle échoue) ;
# 2. construction de l'image agentic-os:<tag> (l'image précédente est conservée) ;
# 3. arrêt de l'app, migrations, démarrage sur <tag> ;
# 4. état enregistré (tag précédent + sauvegarde) pour rollback.sh.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

tag="${1:-}"
[[ "$tag" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "usage : upgrade.sh <tag>"
current=$(state_get current_tag)
[ "$tag" != "$current" ] || die "déjà en version $tag"

backup=$("$(dirname "${BASH_SOURCE[0]}")/backup.sh" "pre-upgrade")
log "sauvegarde pre-upgrade : $backup"

export AOS_IMAGE_TAG="$tag" AOS_VERSION="$tag"
AOS_REVISION="$(git -C "$AOS_REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
export AOS_REVISION
log "construction de agentic-os:$tag"
run_action "$AOS_BUILD"

state_set previous_tag "${current:-}"
state_set rollback_backup "$backup"
log "arrêt, migrations, démarrage en $tag"
run_action "$AOS_APP_STOP"
run_action "$AOS_MIGRATE"
run_action "$AOS_APP_START"
state_set current_tag "$tag"
log "version $tag déployée (schéma $(schema_version)). Retour arrière : deploy/ops/rollback.sh --yes"
