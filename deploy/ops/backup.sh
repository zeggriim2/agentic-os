#!/usr/bin/env bash
# Sauvegarde PostgreSQL d'Agentic OS : archive pg_dump (format custom), vérifiée, empreinte
# SHA-256, métadonnées, rétention. Les fichiers sont créés en 0600 dans un répertoire 0700.
#
# Usage : deploy/ops/backup.sh [libellé]      (libellé : [a-z0-9-], ex. pre-upgrade)
# Sortie : chemin de l'archive sur stdout.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

label="${1:-manual}"
[[ "$label" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || die "libellé invalide : $label"

install -d -m 0700 "$AOS_BACKUP_DIR"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
base="$AOS_BACKUP_DIR/agentic-os-$stamp-$label"
[ ! -e "$base.dump" ] || die "existe déjà : $base.dump"

version=$(schema_version)
log "sauvegarde de $AOS_DB_NAME (schéma $version) → $base.dump"
partial="$base.dump.partial"
trap 'rm -f "$partial"' EXIT
db pg_dump -U "$AOS_DB_USER" -d "$AOS_DB_NAME" --format=custom --compress=6 --no-owner --no-privileges >"$partial"
tables=$(check_archive "$partial")
mv "$partial" "$base.dump"
trap - EXIT
printf '%s  %s\n' "$(sha_of "$base.dump")" "$(basename "$base.dump")" >"$base.dump.sha256"
cat >"$base.json" <<EOF
{"created_at":"$stamp","label":"$label","schema_version":$version,"tables":$tables,"bytes":$(stat -c %s "$base.dump"),"image_tag":"$(state_get current_tag)"}
EOF
chmod 0600 "$base.dump" "$base.dump.sha256" "$base.json"
log "OK : $tables tables, $(stat -c %s "$base.dump") octets, sha256 $(cut -c1-12 <"$base.dump.sha256")"

# Rétention : supprime les archives plus vieilles que AOS_BACKUP_KEEP_DAYS, en gardant toujours
# les AOS_BACKUP_KEEP_MIN plus récentes. Les sauvegardes pre-upgrade/pre-restore suivent la même règle.
mapfile -t all < <(find "$AOS_BACKUP_DIR" -maxdepth 1 -name 'agentic-os-*.dump' -printf '%T@ %p\n' | sort -rn | cut -d' ' -f2-)
for i in "${!all[@]}"; do
	f="${all[$i]}"
	[ "$i" -ge "$AOS_BACKUP_KEEP_MIN" ] || continue
	if [ -n "$(find "$f" -mtime +"$AOS_BACKUP_KEEP_DAYS")" ]; then
		log "rétention : suppression de $(basename "$f")"
		rm -f "$f" "$f.sha256" "${f%.dump}.json"
	fi
done
echo "$base.dump"
