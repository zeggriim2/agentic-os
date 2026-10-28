#!/usr/bin/env bash
# Test de bout en bout des scripts d'exploitation contre un vrai PostgreSQL 16 (Docker) et le
# vrai binaire `agentic-os migrate`. Les actions sur l'application (stop/start/build) sont
# remplacées par un journal pour vérifier leur ordre.
#
# Usage : deploy/ops/test-ops.sh        (prérequis : docker, go)
set -euo pipefail
REPO=$(cd "$(dirname "$0")/../.." && pwd)
# Version de schéma attendue après migration : numéro de la dernière migration embarquée.
LATEST=$(find "$REPO/migrations" -maxdepth 1 -name '[0-9][0-9][0-9][0-9]_*.sql' -printf '%f\n' | sort | tail -1 | cut -c1-4 | sed 's/^0*//')
[ -n "$LATEST" ] || { echo "aucune migration trouvée" >&2; exit 1; }
PORT="${AOS_OPS_TEST_PORT:-55498}"
C="aos-ops-test-$$"
WORK=$(mktemp -d)
cleanup() { docker rm -f "$C" >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

pass=0
ok() { pass=$((pass + 1)); echo "  ✔ $*"; }
fail() { echo "  ✘ $*" >&2; exit 1; }
expect_eq() { if [ "$2" = "$3" ]; then ok "$1"; else fail "$1 : attendu «$3», obtenu «$2»"; fi; }

echo "▶ préparation"
docker run -d --rm --name "$C" -e POSTGRES_DB=agentic_os -e POSTGRES_USER=agentic_os \
	-e POSTGRES_PASSWORD=test -p "127.0.0.1:$PORT:5432" postgres:16-alpine >/dev/null
for _ in $(seq 60); do docker exec "$C" pg_isready -U agentic_os -d agentic_os >/dev/null 2>&1 && break; sleep 1; done
sleep 1
(cd "$REPO" && CGO_ENABLED=0 go build -o "$WORK/agentic-os" ./cmd/agentic-os)
printf 'postgres://agentic_os:test@127.0.0.1:%s/agentic_os?sslmode=disable' "$PORT" >"$WORK/database_url"
chmod 600 "$WORK/database_url"

export AOS_DATA_DIR="$WORK/data" AOS_BACKUP_DIR="$WORK/data/backups" AOS_STATE_FILE="$WORK/data/state.env"
export AOS_DB_EXEC="docker exec -i $C"
APPLOG="$WORK/app.log"
export AOS_APP_STOP="echo stop >>$APPLOG" AOS_APP_START="echo start:\${AOS_IMAGE_TAG:-} >>$APPLOG"
export AOS_BUILD="echo build:\$AOS_IMAGE_TAG >>$APPLOG"
export AOS_MIGRATE="env AOS_DATABASE_URL_FILE=$WORK/database_url AOS_SECRETS_DIR=$WORK AOS_WORKSPACE_ROOT=$WORK/ws $WORK/agentic-os migrate"
OPS="$REPO/deploy/ops"
sql() { docker exec -i "$C" psql -X -q -t -A -v ON_ERROR_STOP=1 -U agentic_os -d "${2:-agentic_os}" -c "$1"; }
features() { sql "SELECT string_agg(title, ',' ORDER BY id) FROM features"; }

# Base au schéma 2 (version précédente) avec des données.
for m in 0001 0002; do docker exec -i "$C" psql -q -v ON_ERROR_STOP=1 -U agentic_os -d agentic_os <"$REPO"/migrations/${m}_*.sql >/dev/null; done
sql "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now()); INSERT INTO schema_migrations VALUES (1),(2)"
sql "INSERT INTO projects(slug,name,repository) VALUES ('demo','Demo','o/demo'); INSERT INTO features(project_id,title,description) VALUES (1,'F-avant','d')"

echo "▶ sauvegarde"
old=$("$OPS/backup.sh" old 2>/dev/null)
[ -f "$old" ] || fail "archive absente"
expect_eq "archive en 0600" "$(stat -c %a "$old")" "600"
expect_eq "répertoire en 0700" "$(stat -c %a "$AOS_BACKUP_DIR")" "700"
(cd "$AOS_BACKUP_DIR" && sha256sum -c --quiet "$(basename "$old").sha256") && ok "empreinte SHA-256 cohérente" || fail "empreinte SHA-256 cohérente"
expect_eq "schéma noté dans les métadonnées" "$(python3 -c "import json;print(json.load(open('${old%.dump}.json'))['schema_version'])")" "2"
if "$OPS/backup.sh" 'Mauvais Libellé' >/dev/null 2>&1; then fail "libellé invalide accepté"; else ok "libellé invalide refusé"; fi

echo "▶ restauration d'une sauvegarde plus ancienne que le schéma courant"
bash -c "$AOS_MIGRATE" >/dev/null 2>&1
expect_eq "migration à jour" "$(sql 'SELECT max(version) FROM schema_migrations')" "$LATEST"
sql "INSERT INTO features(project_id,title,description) VALUES (1,'F-apres','d')"
: >"$APPLOG"
"$OPS/restore.sh" "$old" --yes 2>/dev/null
expect_eq "données de la sauvegarde" "$(features)" "F-avant"
expect_eq "sauvegarde remise au schéma courant" "$(sql 'SELECT max(version) FROM schema_migrations')" "$LATEST"
expect_eq "ordre arrêt → démarrage" "$(tr '\n' ' ' <"$APPLOG")" "stop start: "
safety=$(ls -t "$AOS_BACKUP_DIR"/*-pre-restore.dump | head -1)
docker exec -i "$C" pg_restore -a -t features -f - <"$safety" | grep -q F-apres && ok "état écrasé conservé dans la sauvegarde pre-restore" || fail "état écrasé conservé dans la sauvegarde pre-restore"

echo "▶ refus"
: >"$APPLOG"
bad="$WORK/bad.dump"; cp "$old" "$bad"; cp "$old.sha256" "$bad.sha256"
printf 'X' | dd of="$bad" bs=1 seek=100 conv=notrunc 2>/dev/null
if "$OPS/restore.sh" "$bad" --yes 2>/dev/null; then fail "archive corrompue restaurée"; else ok "archive corrompue refusée"; fi
if "$OPS/restore.sh" "$old" 2>/dev/null; then fail "restauration sans --yes"; else ok "restauration sans --yes refusée"; fi
expect_eq "application non arrêtée lors d'un refus" "$(cat "$APPLOG")" ""
expect_eq "données intactes après refus" "$(features)" "F-avant"

echo "▶ mise à jour puis retour arrière"
: >"$APPLOG"
echo "current_tag=1.0.0" >"$AOS_STATE_FILE"
"$OPS/upgrade.sh" 1.0.1 2>/dev/null
grep -q '^previous_tag=1.0.0$' "$AOS_STATE_FILE" && grep -q '^current_tag=1.0.1$' "$AOS_STATE_FILE" && ok "état de déploiement enregistré" || fail "état de déploiement enregistré"
expect_eq "construction puis arrêt → démarrage en 1.0.1" "$(tr '\n' ' ' <"$APPLOG")" "build:1.0.1 stop start:1.0.1 "
if "$OPS/upgrade.sh" 1.0.1 2>/dev/null; then fail "double mise à jour"; else ok "mise à jour vers la version courante refusée"; fi
sql "INSERT INTO schema_migrations(version) VALUES ($((LATEST + 1)))"
: >"$APPLOG"
if "$OPS/upgrade.sh" 1.0.2 2>/dev/null; then fail "rétrogradation de schéma acceptée"; else ok "rétrogradation de schéma refusée"; fi
expect_eq "application non arrêtée lors d'une rétrogradation refusée" "$(cat "$APPLOG")" ""
sql "DELETE FROM schema_migrations WHERE version = $((LATEST + 1))"
sql "INSERT INTO features(project_id,title,description) VALUES (1,'F-v101','d')"
: >"$APPLOG"
"$OPS/rollback.sh" --yes 2>/dev/null
expect_eq "données d'avant la mise à jour" "$(features)" "F-avant"
expect_eq "redémarrage sur la version précédente" "$(tr '\n' ' ' <"$APPLOG")" "stop start:1.0.0 "
grep -q '^current_tag=1.0.0$' "$AOS_STATE_FILE" && ok "version courante rétablie" || fail "version courante rétablie"
if "$OPS/rollback.sh" --yes 2>/dev/null; then fail "second rollback"; else ok "pas de second retour arrière sans nouvelle mise à jour"; fi

echo "▶ rétention"
for d in 40 35 30; do
	f="$AOS_BACKUP_DIR/agentic-os-old$d-x.dump"; echo x >"$f"; touch -d "$d days ago" "$f"
done
n_before=$(ls "$AOS_BACKUP_DIR"/*.dump | wc -l)
AOS_BACKUP_KEEP_MIN=$((n_before - 1)) "$OPS/backup.sh" retention >/dev/null 2>&1
[ ! -e "$AOS_BACKUP_DIR/agentic-os-old40-x.dump" ] && [ ! -e "$AOS_BACKUP_DIR/agentic-os-old35-x.dump" ] && ok "archives expirées supprimées au-delà du minimum" || fail "archives expirées supprimées au-delà du minimum"
[ -e "$AOS_BACKUP_DIR/agentic-os-old30-x.dump" ] && ok "minimum d'archives conservé même expirées" || fail "minimum d'archives conservé même expirées"

echo "OK : $pass vérifications"
