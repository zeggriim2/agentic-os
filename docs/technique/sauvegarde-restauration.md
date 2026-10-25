# Sauvegarde, restauration, mise à jour et retour arrière

Scripts : `deploy/ops/` — testés en CI (workflow `ops`) contre un vrai PostgreSQL 16 et le vrai
binaire `agentic-os migrate` (`make ops-test`, 23 vérifications).

## Ce qui est sauvegardé

| Élément | Où | Sauvegarde |
| --- | --- | --- |
| Base PostgreSQL (état, jobs, approbations, audit, livraisons webhook) | volume `aos-pgdata` | `backup.sh` |
| Secrets (`/home/hermes/agentic-os-data/secrets`) | hôte | **non** : à conserver hors de la machine (gestionnaire de mots de passe) ; jamais dans les archives |
| Workspaces Git (`/home/hermes/agentic-os-data/workspaces`) | hôte | **non** : reconstructibles (clones et worktrees recréés depuis GitHub ; les branches poussées font foi) |
| Image Docker | registre local | conservée par tag (`agentic-os:<version>`) pour le retour arrière |

## Sauvegarde

```bash
deploy/ops/backup.sh            # ou : make backup
```

- `pg_dump --format=custom` lancé **dans** le conteneur `db` (aucune URL ni mot de passe en argument) ;
- archive écrite en `.partial`, vérifiée (`pg_restore --list`, tables présentes), puis renommée ;
- `…dump.sha256` (empreinte) et `….json` (date, version du schéma, nombre de tables, taille, tag d'image) ;
- répertoire `/home/hermes/agentic-os-data/backups` en 0700, fichiers en 0600 ;
- rétention : suppression au-delà de `AOS_BACKUP_KEEP_DAYS` (14 j), en gardant toujours les
  `AOS_BACKUP_KEEP_MIN` (7) plus récentes.

Planification quotidienne (crontab de l'utilisateur `hermes`, heure de Paris) :

```cron
CRON_TZ=Europe/Paris
30 3 * * * cd /home/hermes/work/agentic-os && deploy/ops/backup.sh daily >>/home/hermes/agentic-os-data/backups/backup.log 2>&1
```

Copie hors machine : synchroniser `/home/hermes/agentic-os-data/backups` vers un stockage externe
(les archives contiennent des données métier : stockage chiffré).

## Restauration

```bash
deploy/ops/restore.sh /home/hermes/agentic-os-data/backups/agentic-os-<date>-<libellé>.dump --yes
# ou : make restore FILE=<archive.dump> CONFIRM=yes
```

1. empreinte SHA-256 et archive vérifiées — sinon refus, **application non arrêtée** ;
2. arrêt de l'application ;
3. sauvegarde `pre-restore` de l'état courant (rien n'est perdu sans trace) ;
4. base recréée, `pg_restore --single-transaction --exit-on-error` ;
5. `agentic-os migrate` : une sauvegarde d'un schéma plus ancien est remise au schéma courant ;
6. redémarrage de l'application.

En cas d'échec après l'arrêt, l'application reste arrêtée et le chemin de la sauvegarde `pre-restore`
est affiché pour revenir à l'état antérieur.

Après une restauration : les jobs `leased` sont repris par la recovery (lease expiré) ; les runs
Hermes actifs au moment de la sauvegarde sont réconciliés (statut relu chez Hermes) ; les livraisons
webhook manquées pendant l'intervalle peuvent être redélivrées depuis GitHub
(*Settings → Webhooks → Recent deliveries*).

## Mise à jour

```bash
git fetch --tags && git checkout v1.0.1
deploy/ops/upgrade.sh 1.0.1
```

Sauvegarde `pre-upgrade` obligatoire (échec = arrêt avant toute modification), construction de
`agentic-os:1.0.1` (l'image précédente est conservée), arrêt, migrations, démarrage. Le tag précédent
et la sauvegarde sont notés dans `/home/hermes/agentic-os-data/deploy-state.env`.

## Retour arrière

```bash
git checkout v1.0.0
deploy/ops/rollback.sh --yes
```

Les migrations sont à sens unique : une ancienne image refuse de démarrer sur un schéma plus récent
(« schéma non à jour »), ce qui empêche un retour arrière silencieusement incohérent. Le retour arrière
restaure donc la sauvegarde `pre-upgrade` puis redémarre l'image précédente. Les données écrites
depuis la mise à jour ne sont plus dans la base, mais restent dans la sauvegarde `pre-restore` créée
automatiquement.

Si la nouvelle version n'a **pas** ajouté de migration, un simple redémarrage sur l'ancienne image suffit
et ne perd rien : `AOS_IMAGE_TAG=1.0.0 docker compose -f deploy/compose.yaml up -d --no-build app`.

## Exercice de restauration

À faire avant la mise en production puis chaque trimestre : restaurer la dernière sauvegarde
quotidienne sur une instance de test (`make ops-test` couvre la mécanique, pas les données réelles),
vérifier `agentic-os doctor` et l'ouverture d'une feature existante dans l'interface.

## Variables

| Variable | Défaut | Rôle |
| --- | --- | --- |
| `AOS_DATA_DIR` | `/home/hermes/agentic-os-data` | racine des données hôte |
| `AOS_BACKUP_DIR` | `$AOS_DATA_DIR/backups` | archives |
| `AOS_BACKUP_KEEP_DAYS` / `AOS_BACKUP_KEEP_MIN` | `14` / `7` | rétention |
| `AOS_STATE_FILE` | `$AOS_DATA_DIR/deploy-state.env` | tags courant/précédent, sauvegarde de rollback |
| `AOS_COMPOSE` | `docker compose -f deploy/compose.yaml` | pilotage de la pile |
