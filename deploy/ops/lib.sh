# shellcheck shell=bash
# Fonctions communes aux scripts d'exploitation (sourcé, pas exécutable seul).
# Toutes les valeurs ont un défaut adapté à deploy/compose.yaml ; les tests les surchargent.
# Aucun secret n'est lu ni affiché ici : l'accès à PostgreSQL passe par le conteneur `db`
# (authentification locale du conteneur), jamais par une URL en argument.

set -euo pipefail
umask 077

AOS_REPO_DIR="${AOS_REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
AOS_DATA_DIR="${AOS_DATA_DIR:-/home/hermes/agentic-os-data}"
AOS_BACKUP_DIR="${AOS_BACKUP_DIR:-$AOS_DATA_DIR/backups}"
AOS_STATE_FILE="${AOS_STATE_FILE:-$AOS_DATA_DIR/deploy-state.env}"
AOS_COMPOSE="${AOS_COMPOSE:-docker compose -f $AOS_REPO_DIR/deploy/compose.yaml}"
# Préfixe pour exécuter une commande dans le conteneur PostgreSQL (stdin/stdout transmis).
AOS_DB_EXEC="${AOS_DB_EXEC:-$AOS_COMPOSE exec -T db}"
AOS_DB_USER="${AOS_DB_USER:-agentic_os}"
AOS_DB_NAME="${AOS_DB_NAME:-agentic_os}"
AOS_BACKUP_KEEP_DAYS="${AOS_BACKUP_KEEP_DAYS:-14}"
AOS_BACKUP_KEEP_MIN="${AOS_BACKUP_KEEP_MIN:-7}"
# Actions sur l'application (surchargées par les tests).
AOS_APP_STOP="${AOS_APP_STOP:-$AOS_COMPOSE stop app}"
AOS_MIGRATE="${AOS_MIGRATE:-$AOS_COMPOSE run --rm --no-deps migrate}"
AOS_APP_START="${AOS_APP_START:-$AOS_COMPOSE up -d --no-build app}"
AOS_BUILD="${AOS_BUILD:-$AOS_COMPOSE build}"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*" >&2; }
die() { log "ERREUR : $*"; exit 1; }

db() { $AOS_DB_EXEC "$@"; }

# Exécute une action configurée (AOS_APP_STOP, AOS_MIGRATE…) ; sa sortie part sur stderr pour
# garder stdout aux résultats des scripts.
run_action() { bash -c "$1" >&2; }

psql_value() {
	db psql -X -q -t -A -v ON_ERROR_STOP=1 -U "$AOS_DB_USER" -d "${2:-$AOS_DB_NAME}" -c "$1"
}

schema_version() {
	psql_value "SELECT COALESCE(max(version),0) FROM schema_migrations" 2>/dev/null || echo 0
}

# Vérifie qu'un fichier est une archive pg_dump lisible (liste de la table des matières).
check_archive() {
	local entries
	entries=$(db pg_restore --list <"$1" | grep -c ' TABLE ' || true)
	[ "${entries:-0}" -gt 0 ] || die "archive illisible ou vide : $1"
	echo "$entries"
}

sha_of() { sha256sum "$1" | cut -d' ' -f1; }

verify_checksum() {
	local f="$1"
	[ -f "$f.sha256" ] || die "empreinte absente : $f.sha256"
	[ "$(cut -d' ' -f1 <"$f.sha256")" = "$(sha_of "$f")" ] || die "empreinte SHA-256 invalide : $f"
}

state_get() { [ -f "$AOS_STATE_FILE" ] && sed -n "s/^$1=//p" "$AOS_STATE_FILE" | tail -1 || true; }

state_set() {
	local tmp
	mkdir -p "$(dirname "$AOS_STATE_FILE")"
	tmp=$(mktemp "$AOS_STATE_FILE.XXXX")
	{ [ -f "$AOS_STATE_FILE" ] && grep -v "^$1=" "$AOS_STATE_FILE" || true; echo "$1=$2"; } >"$tmp"
	mv "$tmp" "$AOS_STATE_FILE"
}
