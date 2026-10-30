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

# Refus de rétrograder : le code à construire doit connaître toutes les migrations déjà appliquées.
# Sinon l'application refuserait de démarrer (« schéma non à jour ») après l'arrêt de la version
# en cours.
latest=$(find "$AOS_REPO_DIR/migrations" -maxdepth 1 -name '[0-9][0-9][0-9][0-9]_*.sql' -printf '%f\n' | sort | tail -1 | cut -c1-4 | sed 's/^0*//')
db_schema=$(schema_version)
[ "${latest:-0}" -ge "${db_schema:-0}" ] ||
	die "rétrogradation refusée : la base est au schéma $db_schema, ce dépôt ne connaît que $latest (version en cours : ${current:-inconnue}). Revenir en arrière : rollback.sh"

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
# Dossier des enregistrements QA (monté par compose) : créé par l'utilisateur hôte, pas par Docker (root).
mkdir -p "$AOS_DATA_DIR/recordings/store" "$AOS_DATA_DIR/recordings/inbox"
run_action "$AOS_APP_START"
state_set current_tag "$tag"
log "version $tag déployée (schéma $(schema_version)). Retour arrière : deploy/ops/rollback.sh --yes"
