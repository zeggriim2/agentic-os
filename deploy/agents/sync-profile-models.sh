#!/usr/bin/env bash
# Applique aux profils Hermes `aos-*` le modèle par défaut choisi dans Agentic OS (page Agents,
# table profile_models). Hermes n'expose pas d'API d'écriture du modèle d'un profil : ce script tourne
# sur l'hôte Hermes, lancé toutes les 30 s par le timer systemd utilisateur `aos-profile-sync`
# (voir install-profile-sync.sh).
#
# Pour chaque demande PENDING : validation des noms, copie de sauvegarde du config.yaml (0600),
# `hermes -p <profil> config set model.provider|model.default`, relecture par `config get`, puis
# APPLIED ou FAILED pour la révision lue (une demande plus récente reste PENDING). En cas d'échec, le
# config.yaml d'origine est remis en place. Chaque passage note un battement (profile_sync_status).
#
# Aucun secret n'est lu : PostgreSQL est joint par le conteneur `db` (deploy/ops/lib.sh).
# Variables : AOS_HERMES_BIN (chemin absolu de hermes, obligatoire), HERMES_HOME (défaut ~/.hermes),
# AOS_PROFILE_BACKUP_DIR (défaut $HERMES_HOME/backups/aos-profile-models).
# shellcheck source=../ops/lib.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../ops/lib.sh"

HERMES_HOME_DIR="${HERMES_HOME:-$HOME/.hermes}"
BACKUP_DIR="${AOS_PROFILE_BACKUP_DIR:-$HERMES_HOME_DIR/backups/aos-profile-models}"
HERMES_BIN="${AOS_HERMES_BIN:-}"
case "$HERMES_BIN" in
/*) [ -x "$HERMES_BIN" ] || die "AOS_HERMES_BIN non exécutable : $HERMES_BIN" ;;
*) die "AOS_HERMES_BIN doit être le chemin absolu de hermes" ;;
esac

PROFILE_RE='^aos-[a-z0-9][a-z0-9_-]{0,59}$'
PROVIDER_RE='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
MODEL_RE='^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$'

# </dev/null : `docker exec -i` lirait sinon la liste des demandes en cours de lecture.
q() { psql_value "$1" </dev/null; }

# Texte d'erreur sûr pour SQL : caractères imprimables, apostrophes doublées, 300 caractères max.
sql_text() {
	local s
	s=$(printf '%s' "$1" | tr '\n\t' '  ' | tr -cd '[:print:]' | cut -c1-300)
	printf '%s' "${s//\'/\'\'}"
}

hermes_cfg() { "$HERMES_BIN" -p "$1" config "${@:2}" </dev/null; }

record() { # profil révision état [erreur]
	local err="NULL"
	[ -n "${4:-}" ] && err="'$(sql_text "$4")'"
	q "UPDATE profile_models SET state='$3', error=$err, applied_at=CASE WHEN '$3'='APPLIED' THEN now() ELSE NULL END
		WHERE profile='$1' AND revision=$2 AND state='PENDING'" >/dev/null
}

# apply profil fournisseur modèle révision : 0 = appliqué ; sinon message d'erreur sur stdout.
apply() {
	local profile=$1 provider=$2 model=$3 revision=$4 cfg backup out got_p got_m
	cfg="$HERMES_HOME_DIR/profiles/$profile/config.yaml"
	[ -f "$cfg" ] || { echo "profil introuvable sur l'hôte ($cfg)"; return 1; }
	mkdir -p "$BACKUP_DIR" && chmod 700 "$BACKUP_DIR"
	backup="$BACKUP_DIR/$profile-$(date -u +%Y%m%dT%H%M%SZ)-r$revision.yaml"
	cp "$cfg" "$backup" && chmod 600 "$backup"
	if ! out=$(hermes_cfg "$profile" set model.provider "$provider" 2>&1) ||
		! out=$(hermes_cfg "$profile" set model.default "$model" 2>&1); then
		cp "$backup" "$cfg"
		echo "hermes config set a échoué : $out"
		return 1
	fi
	got_p=$(hermes_cfg "$profile" get model.provider 2>/dev/null | tail -1) || got_p=""
	got_m=$(hermes_cfg "$profile" get model.default 2>/dev/null | tail -1) || got_m=""
	if [ "$got_p" != "$provider" ] || [ "$got_m" != "$model" ]; then
		cp "$backup" "$cfg"
		echo "relecture différente : $got_p/$got_m au lieu de $provider/$model (config d'origine remise)"
		return 1
	fi
}

host=$(hostname 2>/dev/null | tr -cd 'A-Za-z0-9.-' | cut -c1-63)
q "INSERT INTO profile_sync_status(id, last_seen_at, host) VALUES (1, now(), '$host')
	ON CONFLICT (id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at, host=EXCLUDED.host" >/dev/null

pending=$(q "SELECT profile||' '||desired_provider||' '||desired_model||' '||revision FROM profile_models WHERE state='PENDING' ORDER BY profile")
applied=0 failed=0
while read -r profile provider model revision; do
	[ -n "${profile:-}" ] || continue
	if ! [[ "$profile" =~ $PROFILE_RE && "$provider" =~ $PROVIDER_RE && "$model" =~ $MODEL_RE && "$revision" =~ ^[0-9]+$ ]]; then
		log "demande invalide ignorée : $profile"
		[[ "$profile" =~ $PROFILE_RE && "$revision" =~ ^[0-9]+$ ]] && record "$profile" "$revision" FAILED "noms invalides"
		failed=$((failed + 1))
		continue
	fi
	if msg=$(apply "$profile" "$provider" "$model" "$revision"); then
		record "$profile" "$revision" APPLIED
		log "$profile : $provider/$model appliqué (révision $revision)"
		applied=$((applied + 1))
	else
		record "$profile" "$revision" FAILED "$msg"
		log "$profile : échec — $msg"
		failed=$((failed + 1))
	fi
done <<<"$pending"
[ $((applied + failed)) -eq 0 ] || log "terminé : $applied appliqué(s), $failed échec(s)"
