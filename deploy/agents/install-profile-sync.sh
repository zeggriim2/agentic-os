#!/usr/bin/env bash
# Installe le timer systemd utilisateur `aos-profile-sync` : il lance sync-profile-models.sh toutes les
# 30 s pour appliquer aux profils Hermes aos-* le modèle choisi dans Agentic OS (page Agents).
#
# Usage (sur l'hôte Hermes, utilisateur hermes) :
#   deploy/agents/install-profile-sync.sh              installe/met à jour et lance un premier passage
#   deploy/agents/install-profile-sync.sh --uninstall  arrête et retire le timer
# Le chemin de hermes est résolu ici une fois (absolu) puis figé dans l'unité ; AOS_HERMES_BIN le force.
# Le timer survit à la déconnexion si le « linger » systemd est actif (loginctl enable-linger hermes).
set -euo pipefail
REPO=$(cd "$(dirname "$0")/../.." && pwd)
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
NAME=aos-profile-sync

if [ "${1:-}" = "--uninstall" ]; then
	systemctl --user disable --now "$NAME.timer" 2>/dev/null || true
	rm -f "$UNIT_DIR/$NAME.service" "$UNIT_DIR/$NAME.timer"
	systemctl --user daemon-reload
	echo "timer $NAME retiré"
	exit 0
fi

HERMES_BIN="${AOS_HERMES_BIN:-$(command -v hermes || true)}"
case "$HERMES_BIN" in
/*) [ -x "$HERMES_BIN" ] || { echo "hermes non exécutable : $HERMES_BIN" >&2; exit 1; } ;;
*) echo "hermes introuvable : définir AOS_HERMES_BIN=/chemin/absolu/hermes" >&2; exit 1 ;;
esac

mkdir -p "$UNIT_DIR"
cat >"$UNIT_DIR/$NAME.service" <<EOF
[Unit]
Description=Agentic OS : modèle par défaut des profils Hermes aos-*

[Service]
Type=oneshot
Environment=AOS_HERMES_BIN=$HERMES_BIN
Environment=PATH=$(dirname "$HERMES_BIN"):/usr/local/bin:/usr/bin:/bin
ExecStart=$REPO/deploy/agents/sync-profile-models.sh
EOF
cat >"$UNIT_DIR/$NAME.timer" <<EOF
[Unit]
Description=Agentic OS : synchronisation des modèles de profils toutes les 30 s

[Timer]
OnBootSec=1min
OnUnitActiveSec=30s
AccuracySec=5s

[Install]
WantedBy=timers.target
EOF
systemctl --user daemon-reload
systemctl --user enable --now "$NAME.timer" >/dev/null
systemctl --user start "$NAME.service"
echo "timer $NAME installé ($UNIT_DIR) ; hermes : $HERMES_BIN"
systemctl --user list-timers "$NAME.timer" --no-pager | head -2
