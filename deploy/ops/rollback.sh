#!/usr/bin/env bash
# Retour à la version précédente après une mise à jour (upgrade.sh).
#
# Usage : deploy/ops/rollback.sh --yes
#
# Les migrations sont à sens unique : une ancienne image refuse de démarrer sur un schéma plus
# récent (« schéma non à jour »). Le retour arrière restaure donc la sauvegarde pre-upgrade
# (les données écrites depuis la mise à jour sont perdues, mais conservées dans la sauvegarde
# pre-restore créée automatiquement) puis redémarre l'image précédente.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[ "${1:-}" = "--yes" ] || die "retour arrière destructif : relancer avec --yes"
prev=$(state_get previous_tag)
backup=$(state_get rollback_backup)
current=$(state_get current_tag)
[ -n "$prev" ] || die "aucune version précédente enregistrée dans $AOS_STATE_FILE"
[ -n "$backup" ] && [ -f "$backup" ] || die "sauvegarde pre-upgrade introuvable : ${backup:-?}"

log "retour de $current vers $prev avec $(basename "$backup")"
export AOS_IMAGE_TAG="$prev"
"$(dirname "${BASH_SOURCE[0]}")/restore.sh" "$backup" --yes
state_set current_tag "$prev"
state_set previous_tag ""
state_set rollback_backup ""
log "version $prev rétablie"
