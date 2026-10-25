#!/usr/bin/env bash
# Restauration d'une sauvegarde Agentic OS.
#
# Usage : deploy/ops/restore.sh <archive.dump> --yes
#
# Étapes : empreinte et archive vérifiées → arrêt de l'app → sauvegarde de sécurité de l'état
# courant (pre-restore) → base recréée → pg_restore en une transaction → migrations (une
# sauvegarde plus ancienne est remise au schéma courant) → redémarrage de l'app.
# En cas d'échec après l'arrêt, l'app reste arrêtée et la sauvegarde pre-restore est indiquée.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

file="${1:-}"
[ -n "$file" ] && [ -f "$file" ] || die "usage : restore.sh <archive.dump> --yes"
[ "${2:-}" = "--yes" ] || die "restauration destructive : relancer avec --yes"
file=$(readlink -f "$file")

verify_checksum "$file"
tables=$(check_archive "$file")
log "archive valide : $tables tables"

log "arrêt de l'application"
run_action "$AOS_APP_STOP"

safety=$("$(dirname "${BASH_SOURCE[0]}")/backup.sh" pre-restore)
log "état courant sauvegardé : $safety"
trap 'log "ÉCHEC : application laissée arrêtée. État d'"'"'avant restauration : $safety"' ERR

log "recréation de la base $AOS_DB_NAME"
psql_value "DROP DATABASE IF EXISTS \"$AOS_DB_NAME\" WITH (FORCE)" postgres >/dev/null
psql_value "CREATE DATABASE \"$AOS_DB_NAME\" OWNER \"$AOS_DB_USER\"" postgres >/dev/null

log "restauration"
db pg_restore -U "$AOS_DB_USER" -d "$AOS_DB_NAME" --no-owner --no-privileges --exit-on-error --single-transaction <"$file"
before=$(schema_version)

log "migrations (schéma restauré : $before)"
run_action "$AOS_MIGRATE"
after=$(schema_version)
log "schéma : $before → $after"

log "redémarrage de l'application"
run_action "$AOS_APP_START"
trap - ERR
log "restauration terminée depuis $(basename "$file")"
