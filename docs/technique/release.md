# Publication d'une version

Workflow GitFlow : [gitflow.md](gitflow.md). Ce document est la checklist de la **v1.0.0** et des
versions suivantes. Rien n'est publié ni déployé automatiquement sans les étapes humaines ci-dessous.

## 1. Gel du périmètre (sur `develop`)

- [ ] Toutes les PR prévues pour la version sont mergées dans `develop`.
- [ ] Sur le dernier commit de `develop`, tous les checks sont verts : `tests`, `e2e`, `govulncheck`,
      `npm audit`, `backup-restore`.
- [ ] `govulncheck ./...` et `npm audit` relancés le jour même (nouvelles CVE).

## 2. Branche de release

```bash
git fetch origin && git switch -c release/1.0.0 origin/develop
```

- [ ] `CHANGELOG.md` : renommer `## [Non publié] — 1.0.0` en `## [1.0.0] — AAAA-MM-JJ`.
- [ ] `web/package.json` : `"version": "1.0.0"` (déjà le cas pour la 1.0.0).
- [ ] PR draft `release/1.0.0` → `main` ; seules les corrections bloquantes sont acceptées ensuite.

## 3. Vérifications de release (sur `release/1.0.0`)

- [ ] CI verte (mêmes checks que §1).
- [ ] Image construite localement : `make docker`, puis `docker run --rm agentic-os:dev version`.
- [ ] Scan ponctuel des paquets de l'image (aucun scanner tiers en CI) :
      `docker run --rm -v /var/run/docker.sock:/var/run/docker.sock anchore/grype:<version épinglée> agentic-os:dev --only-fixed`
      → aucune vulnérabilité corrigeable ; les vulnérabilités sans correctif sont listées dans la PR de release.
- [ ] Exercice de restauration : `make ops-test`, et restauration réelle de la dernière sauvegarde sur
      une instance de test (voir sauvegarde-restauration.md).
- [ ] Recette manuelle courte sur une instance de test reliée au **vrai Hermes** et à un dépôt
      **sandbox** : une feature jusqu'à la PR draft, merge humain, ticket DONE. (La suite e2e utilise
      des faux Hermes/GitHub ; cette recette couvre les intégrations réelles.)
- [ ] `agentic-os doctor` vert sur l'instance cible.

## 4. Publication (humain)

1. Merger la PR `release/1.0.0` → `main` (merge commit, pas de squash).
2. Tag annoté sur le commit de `main` :
   ```bash
   git switch main && git pull --ff-only
   git tag -a v1.0.0 -m "Agentic OS 1.0.0" && git push origin v1.0.0
   ```
3. Le workflow `release` vérifie que le tag est annoté, sur `main` et présent dans le CHANGELOG,
   publie `ghcr.io/zeggriim2/agentic-os:1.0.0` (SBOM et provenance) et crée la release GitHub avec
   les notes du CHANGELOG et le digest.
4. Rapatrier `main` dans `develop` (PR `main` → `develop`) pour récupérer les commits de release.

## 5. Déploiement (humain)

```bash
cd /home/hermes/work/agentic-os && git fetch --tags && git checkout v1.0.0
deploy/ops/upgrade.sh 1.0.0        # sauvegarde pre-upgrade, migrations, démarrage
docker compose -f deploy/compose.yaml run --rm --no-deps app doctor
```

Retour arrière : `deploy/ops/rollback.sh --yes` (voir sauvegarde-restauration.md).

## 6. Protection des branches (réglage GitHub, droits admin — une fois)

- `main` et `develop` : PR obligatoire, pas de push direct ni de force-push, branche à jour avant merge,
  au moins une revue humaine, conversations résolues.
- Checks requis : `tests`, `e2e`, `govulncheck`, `npm audit` (`backup-restore` ne tourne que si
  `deploy/`, `migrations/`, `internal/store/` ou `cmd/` changent : ne pas le rendre requis, le
  vérifier en §1).
- Tags `v*` : création réservée aux administrateurs (règle de protection des tags).
