# Tests E2E navigateur et portes de release

Ce document décrit la suite E2E (`e2e/`) : ce qu'elle couvre, l'architecture des faux services, comment
la lancer en local et en CI, et ce qu'elle ne couvre pas.

## 1. Ce qui est couvert

La suite pilote un vrai Chromium (Playwright) contre le **vrai binaire** `agentic-os serve` (UI embarquée,
API, moteur de jobs, PostgreSQL réel) ; seuls Hermes, GitHub et Telegram sont simulés.

| Fichier | Couverture |
|---|---|
| `tests/01-auth.spec.ts` | connexion refusée (mauvais mot de passe), acceptée, déconnexion (session détruite côté serveur, retour à `/login`) |
| `tests/02-happy-path.spec.ts` | **parcours complet par l'UI** : création et activation du projet (`o/demo`, base `main`, check requis `tests`) → feature → démarrage de la spec → run PO observé **en direct** dans le détail du run (événements SSE reçus sans rechargement, deltas non persistés) → approbation `spec_review` → `design_review` (1 issue créée dans le faux GitHub avec le marqueur) → `dev_gate` → développeur (commit réel dans le worktree), Reviewer, QA → approbation `publish_pr` → ticket `PR_OPENED`, PR **draft** vérifiée dans le faux GitHub (head `aos/<clé>`, base `main`, SHA = `head_sha` affiché, `Closes #<issue>`) → webhook `check_run` signé → `HUMAN_MERGE` → merge (fast-forward réel du dépôt bare) + webhook `pull_request` closed/merged signé → ticket et feature `DONE`, notifications Telegram reçues |
| `tests/03-rejection.spec.ts` | rejet de spec : commentaire obligatoire (contrôle UI), rejet, retour du feedback au PO (spec révisée), nouvelle revue, audit du commentaire ; rejet du feu vert : feature et tickets `CANCELLED`, aucune PR |
| `tests/04-dispatch.spec.ts` | pause du dispatch depuis la vue d'ensemble, alerte affichée, aucun job pris pendant la pause, reprise depuis l'alerte, le job repart |
| `tests/05-keyboard.spec.ts` | clavier seul : connexion (focus initial, Tab, Entrée, retour à la page demandée), lien d'évitement, décision d'approbation ; indicateur de focus visible vérifié à chaque étape |
| `tests/06-responsive.spec.ts` | à 320, 768 et 1440 px : vue d'ensemble, projets, features, détail de feature, approbations, détail d'approbation, détail de ticket, détail de run, agents, audit — aucun défilement horizontal (`scrollWidth <= clientWidth`), navigation principale accessible (tiroir mobile sous 1024 px) |
| `tests/07-accessibility.spec.ts` | `@axe-core/playwright` (WCAG 2.1 A/AA + bonnes pratiques) sur les mêmes pages, thèmes sombre et clair, menu mobile et page de connexion : **échec sur toute violation `serious` ou `critical`** |

Les libellés vérifiés sont ceux de `web/src/lib/states.ts` ; les états sont aussi lus via l'attribut
`data-state` des badges. Les noms d'approbation sont ceux de `internal/domain` (`spec_review`,
`design_review`, `dev_gate`, `publish_pr`).

## 2. Architecture

```
Playwright (Chromium) ──► agentic-os serve (127.0.0.1:<port libre>) ──► PostgreSQL 16 (conteneur jetable)
                                  │  AOS_HERMES_URL / AOS_GITHUB_API_URL / AOS_TELEGRAM_API_URL
                                  ▼
                          aos-e2e-fakes (127.0.0.1:<port libre>)   e2e/fakes/main.go
                                  │  dépôts bare : <tmp>/remotes/<owner>/<repo>.git
                                  └─ AOS_GITHUB_WEB_URL=file://<tmp>/remotes  (clone/push git réels)
```

`support/global-setup.ts` :

1. construit l'UI (`web` : `npm ci && npm run build`) et les binaires `agentic-os` et `aos-e2e-fakes`
   (ou réutilise `AOS_E2E_BIN_DIR`) ;
2. écrit les secrets dans un dossier temporaire `0700` (fichiers `0600`) : `session_key`, `github_token`,
   `github_webhook_secret`, `telegram_bot_token`, `telegram_chat_id`, une clé par profil Hermes
   (`hermes/aos-*.key`) et `admin_password_hash` produit par `agentic-os admin-password` (mot de passe
   aléatoire passé sur l'entrée standard) ;
3. démarre `postgres:16-alpine` (`docker run --rm`, port loopback), attend `pg_isready`, lance `agentic-os migrate` ;
4. démarre les faux services puis `agentic-os serve`, attend `/healthz` et `/readyz` ;
5. ouvre **une** session administrateur réutilisée par les tests (`.auth/admin.json`) : la connexion est
   limitée à 5 tentatives par minute et par IP, la suite en consomme 4 au total.

La fonction renvoyée sert de teardown : arrêt des processus, `docker rm -f`, suppression du dossier
temporaire (`AOS_E2E_KEEP=1` pour le conserver). Les journaux restent dans `e2e/logs/`.

Les tests partagent un seul serveur : ils s'exécutent en série (`workers: 1`) et chacun crée ses propres
projets (`demo`, `rejet`, `divers`) et features. Aucun nouvel essai automatique (`retries: 0`).

### Faux services (`e2e/fakes`, paquet `main` du module Go)

- **Hermes** (`/p/<profil>/v1/...`) : authentification par la clé du profil lue dans les secrets
  d'Agentic OS ; `POST /runs` idempotent (`Idempotency-Key` : même clé + même charge → même `run_id`,
  `replayed:true` ; charge différente → `409 idempotency_key_conflict`) ; `GET /runs/{id}`, `POST .../stop`,
  `GET .../events` (SSE `data: {"event","seq",...}`, fermé après `run.completed`/`run.failed`),
  `capabilities`, `models`. Le rôle est déduit des instructions ; chaque run émet
  `reasoning.available`, `tool.started`, `message.delta`, `tool.completed`, `run.completed` et produit un
  livrable valide pour son rôle :
  - PO : spec (révisée si un `human_feedback` est présent) ; Tech Lead : 1 ticket, 2 critères ;
  - **Développeur** : lit `Worktree :`/`Branche :`/`Clé :` dans l'entrée, refuse tout chemin hors
    `AOS_WORKSPACE_ROOT`, vérifie la branche, écrit `e2e-<clé>.md` et **commite réellement** (sans push) ;
  - Reviewer : `approved` au SHA exact ; QA : vérifie réellement le fichier au SHA exact et reprend chaque
    critère d'acceptation du ticket.
- **GitHub** (`/repos/...`, `/search/issues`, `/rate_limit`) : jeton bot vérifié ; issues en mémoire
  (recherche par marqueur) ; branches lues dans les dépôts bare (`git rev-parse`) ; PR (liste filtrée
  head/base, création refusée si la branche manque ou si une PR ouverte existe, head SHA relu tant que la
  PR est ouverte) ; `check-runs` (checks configurés en `success`, surchargeables) et `status`.
- **Telegram** (`/bot<token>/getMe|sendMessage`) : jeton vérifié, messages conservés (jamais journalisés).
- **Contrôle** (`/__e2e/...`) : `state`, `hold`/`release` d'un rôle (le run attend : observation du flux
  en direct), `checks`, `webhook/check-run` et `merge` (webhooks signés `X-Hub-Signature-256` avec le
  secret d'Agentic OS, envoyés à `/webhooks/github` ; la réponse d'Agentic OS est renvoyée au test).

## 3. Lancer en local

Prérequis : Go (version de `go.mod`), Node 22, Docker, `git` (`/usr/bin/git`, sinon `AOS_E2E_GIT`).

```sh
cd e2e && npm ci && npx playwright install chromium && npx playwright test
```

Options : `AOS_E2E_SKIP_WEB_BUILD=1` (réutiliser `web/dist`), `AOS_E2E_BIN_DIR=<dossier>` (binaires
prébuilds), `AOS_E2E_PG_PORT=<port>`, `AOS_E2E_KEEP=1`. Rapport : `npx playwright show-report`.
Sous Linux sans dépendances système de Chromium : `npx playwright install --with-deps chromium` (root).

## 4. Portes de release

Une version n'est publiable que si **tous** ces checks sont verts sur le SHA exact :

| Check (nom du job) | Workflow | Contenu |
|---|---|---|
| `tests` | `.github/workflows/tests.yml` | gofmt, `go vet`, tests unitaires et d'intégration Go (PostgreSQL), build + tests de l'UI |
| `e2e` | `.github/workflows/e2e.yml` | cette suite (Chromium) |
| `security` | workflow dédié (PR séparée) | analyses de sécurité |
| `ops` | workflow dédié (PR séparée) | image Docker, Compose, exploitation |

Le job `e2e` épingle ses actions par SHA, n'a que `contents: read` et publie le rapport Playwright, les
traces et les journaux (`e2e/logs/`) en artefact en cas d'échec.

## 5. Ce qui n'est pas couvert

- **Hermes réel** : modèles, outils, profils et contrat live (`/v1/runs` réel, arrêt effectif, budgets
  réels) — vérifiés par la recette sandbox, pas ici. Les livrables simulés sont toujours valides : les
  chemins d'erreur des livrables sont couverts par les tests Go (`internal/engine`).
- **GitHub réel** : permissions du token, protections de branche, vraie CI, latence/limites de l'API,
  webhooks livrés par GitHub. Le push passe par `file://` (pas d'authentification HTTPS/`GIT_ASKPASS`).
- **Telegram réel** : seul le format `sendMessage`/`getMe` est simulé.
- Navigateurs autres que Chromium, déploiement Docker/Compose, reprise après redémarrage, concurrence de
  plusieurs workers sur des projets distincts, rework (changements demandés) de bout en bout.
