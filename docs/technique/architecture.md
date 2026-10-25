# Agentic OS — Architecture technique (contrat V1)

Ce document est **le contrat** entre le backend Go, le frontend et l'exploitation.
Toute divergence doit être corrigée ici d'abord.

## 1. Vue d'ensemble

```
Navigateur ──(tunnel SSH)──► 127.0.0.1:8788  agentic-os (binaire Go unique)
                                   │  ├─ API REST /api/* + SSE /api/events
                                   │  ├─ UI statique (web/dist embarquée via go:embed)
                                   │  ├─ worker(s) : file de jobs PostgreSQL
                                   │  └─ pollers : runs Hermes, PR/CI GitHub
                                   ├──► PostgreSQL 16 (conteneur, 127.0.0.1:55433)
                                   ├──► Hermes API 127.0.0.1:8642 (/p/<profil>/v1/runs)
                                   ├──► GitHub REST (token bot) + webhooks HMAC
                                   └──► Telegram Bot API (notifications)
```

- Le conteneur `app` tourne en `network_mode: host` (Hermes écoute en loopback) et écoute **uniquement**
  `127.0.0.1:${AOS_HTTP_PORT:-8788}`. UID/GID 1000, non-root, `read_only`, `no-new-privileges`.
- Les workspaces Git sont dans `${AOS_WORKSPACE_ROOT}` (défaut `/home/hermes/agentic-os-data/workspaces`),
  monté **au même chemin** dans le conteneur : Hermes (qui exécute ses outils sur l'hôte) voit les mêmes chemins.

## 2. Arborescence

```
cmd/agentic-os/main.go         # sous-commandes: serve | migrate | doctor | admin-password
internal/config                # lecture env + fichiers secrets
internal/domain                # entités, machines à états (pures, testées)
internal/store                 # pgx + migrations (embed), requêtes
internal/engine                # file de jobs, worker, leases, recovery, handlers par type de job
internal/hermes                # client API Hermes (runs, status, events, stop)
internal/github                # client REST GitHub + webhooks
internal/gitops                # clone, worktree, push, vérifs (exec git, sans shell)
internal/notify                # Telegram
internal/httpapi               # routeur chi, auth, CSRF, handlers, SSE hub
internal/audit                 # écriture append-only
web/                           # React + TS + Vite + Tailwind ; build → web/dist (embed)
migrations/                    # *.sql (goose-like, appliquées par `migrate`)
deploy/                        # Dockerfile, compose.yaml, scripts
docs/produit, docs/technique
```

Stack : Go 1.23, `github.com/go-chi/chi/v5`, `github.com/jackc/pgx/v5`, logs `log/slog` JSON.
Pas d'ORM. Frontend : React 18, TypeScript strict, Vite, Tailwind, TanStack Query, React Router,
lucide-react. Aucun appel externe depuis le navigateur (tout passe par `/api`).

## 3. Configuration (env)

| Variable | Défaut | Rôle |
|---|---|---|
| `AOS_HTTP_ADDR` | `127.0.0.1:8788` | écoute HTTP |
| `AOS_DATABASE_URL_FILE` | — | fichier contenant le DSN Postgres |
| `AOS_SECRETS_DIR` | `/run/secrets/agentic-os` | dossier des secrets |
| `AOS_WORKSPACE_ROOT` | `/home/hermes/agentic-os-data/workspaces` | clones et worktrees |
| `AOS_HERMES_URL` | `http://127.0.0.1:8642` | API Hermes |
| `AOS_WORKERS` | `2` | concurrence |
| `AOS_LEASE_SECONDS` | `60` | durée de lease (heartbeat toutes les 20 s) |
| `AOS_MAX_REWORK` | `3` | reworks max par ticket |

Secrets (fichiers dans `AOS_SECRETS_DIR`, jamais en variables d'env, jamais loggés, jamais renvoyés) :
`github_token`, `github_webhook_secret`, `telegram_bot_token`, `telegram_chat_id`,
`session_key` (32+ octets), `admin_password_hash` (bcrypt), `hermes/<profil>.key` (API_SERVER_KEY du profil).

## 4. Modèle de données (PostgreSQL)

Toutes les tables ont `id BIGSERIAL`, `created_at`, `updated_at TIMESTAMPTZ` (UTC) sauf mention.
Table `schema_migrations(version int primary key, applied_at)`.

- `projects` : `slug` unique, `name`, `repository` (`owner/repo`), `base_branch`, `required_checks text[]`,
  `mode` (`sandbox|product`), `enabled bool`, `test_command text` (indicatif pour les agents).
- `agents` : `role` unique (`po|tech_lead|developer|reviewer|qa`), `hermes_profile`, `desired_model`,
  `observed_model`, `observed_provider`, `sync_state` (`SYNCED|DRIFT_DETECTED|UNKNOWN|UNREACHABLE`),
  `observed_at`, `max_tokens_per_run int`, `enabled bool`, `instructions_revision int`.
- `features` : `project_id`, `title`, `description`, `state`, `state_reason`, `spec jsonb`, `design jsonb`,
  `version int` (verrou optimiste).
- `tickets` : `feature_id`, `project_id`, `key` unique (`<slug>-F<feature>-T<n>`), `title`, `body`,
  `acceptance jsonb`, `state`, `state_reason`, `issue_number`, `issue_url`, `branch`, `head_sha`,
  `attempt int`, `pr_number`, `pr_url`, `ci_state` (`unverified|pending|success|failure`), `version`.
- `jobs` : `kind`, `subject_type` (`feature|ticket|approval`), `subject_id`, `status`
  (`queued|leased|succeeded|failed|cancelled`), `attempts`, `max_attempts`, `run_after`,
  `lease_owner`, `lease_expires_at`, `idempotency_key` unique, `last_error`, `error_class`
  (`timeout|budget|policy|provider|invalid_output|external|internal`), `payload jsonb`.
- `runs` : `job_id`, `agent_role`, `hermes_profile`, `hermes_run_id` unique nullable, `status`
  (`submitted|running|completed|failed|stopped|lost`), `input_tokens`, `output_tokens`, `total_tokens`,
  `model`, `provider`, `output text` (borné 256 KiB), `artifact jsonb`, `error`, `started_at`, `finished_at`.
- `run_events` : `run_id`, `seq int`, `event text`, `data jsonb` (borné), unique(`run_id`,`seq`).
- `approvals` : `kind` (`spec_review|design_review|dev_gate|rework|publish_pr`), `subject_type`,
  `subject_id`, `title`, `effect text` (ce que l'approbation autorise, en clair), `details jsonb`,
  `status` (`pending|approved|rejected|executed|execution_failed|expired`), `decided_by`, `decided_at`,
  `decision_comment`, `executed_at`, `execution_result jsonb`. Une seule `pending` par (kind, subject).
- `audit_log` (append-only, pas d'`updated_at`, aucun UPDATE/DELETE applicatif ; trigger qui refuse
  UPDATE/DELETE) : `at`, `actor` (`user:<login>|system|agent:<role>`), `action`, `subject_type`,
  `subject_id`, `before jsonb`, `after jsonb`, `run_id`, `sha`.
- `github_deliveries` : `delivery_id` unique, `event`, `received_at`, `processed bool` (déduplication).
- `settings` : `key` unique, `value jsonb` (ex. `dispatch_paused`).
- `sessions` : `token_hash` unique, `login`, `csrf_token`, `expires_at`.

## 5. Machines à états (internal/domain)

Les transitions sont des fonctions pures `Apply(state, event) (newState, error)` ; toute transition non
listée est refusée. Chaque transition persistée écrit une entrée d'audit dans la même transaction.

### Feature
| Depuis | Événement | Vers | Effet |
|---|---|---|---|
| IDEA | `start_spec` (user) | SPEC | job `po_spec` |
| SPEC | `spec_ready` (job) | SPEC_REVIEW | approval `spec_review` |
| SPEC | `spec_failed` | BLOCKED | |
| SPEC_REVIEW | approval approved | TECH_DESIGN | job `tech_design` |
| SPEC_REVIEW | approval rejected (commentaire obligatoire) | SPEC | job `po_spec` avec feedback |
| TECH_DESIGN | `design_ready` | TECH_DESIGN_REVIEW | approval `design_review` (effet : « créer N issues sur owner/repo ») |
| TECH_DESIGN_REVIEW | approved | ISSUES_OPENED | job `open_issues` |
| TECH_DESIGN_REVIEW | rejected | TECH_DESIGN | job `tech_design` avec feedback |
| ISSUES_OPENED | `issues_opened` | HUMAN_GATE | approval `dev_gate` |
| HUMAN_GATE | approved | READY_FOR_DEV | tickets → READY_FOR_DEV, job `develop` pour chaque ticket (séquentiel par projet) |
| READY_FOR_DEV | 1er ticket démarré | IN_DEV | |
| IN_DEV | tous tickets DONE/CANCELLED | DONE | |
| * (non terminal) | `cancel` (user) | CANCELLED | jobs annulés, runs stoppés |
| BLOCKED | `retry` (user) | état précédent | rejoue le job |

`NEEDS_CLARIFICATION` : si le livrable PO contient `open_questions` non vides, la feature va en
SPEC_REVIEW quand même ; l'approbation affiche les questions et le rejet avec commentaire sert de réponse.

### Ticket
| Depuis | Événement | Vers | Effet |
|---|---|---|---|
| READY_FOR_DEV | job `develop` démarre | DEV | worktree + run developer |
| DEV | `dev_pushed` (sha vérifié) | REVIEW | job `review` |
| REVIEW | verdict `approved` | QA | job `qa` |
| REVIEW | verdict `changes_requested` | CHANGES_REQUESTED | approval `rework` |
| QA | verdict `passed` | AWAITING_PR_APPROVAL | approval `publish_pr` |
| QA | verdict `failed` | CHANGES_REQUESTED | approval `rework` |
| CHANGES_REQUESTED | rework approved et attempt < MAX | DEV | job `develop` (feedback = findings) attempt+1 |
| CHANGES_REQUESTED | rejected | CANCELLED | |
| AWAITING_PR_APPROVAL | approved | PR_OPENED | job `publish_pr` (PR draft, relecture exacte) |
| PR_OPENED | CI success sur head_sha | HUMAN_MERGE | notification |
| PR_OPENED / HUMAN_MERGE | PR mergée (webhook/poll) | DONE | |
| PR_OPENED / HUMAN_MERGE | PR fermée sans merge | CANCELLED | |
| DEV/REVIEW/QA | erreur job non récupérable | BLOCKED | |
| BLOCKED | `retry` | état précédent | |

Règles fail-closed :
- Un verdict n'est accepté que si le run Hermes est `completed`, `interrupted=false`, `partial=false`,
  et que le JSON du livrable est valide (clés exactes, verdict dans l'allowlist, `sha` == `head_sha`).
- Reviewer et QA travaillent dans un worktree **détaché** au SHA exact ; avant le run, le SHA distant de la
  branche doit être égal à `head_sha` ; après le run, le worktree doit être propre et toujours à ce SHA.
- Le développeur ne pousse pas : il commit. Agentic OS vérifie (`git status --porcelain` vide, HEAD ≠ base,
  diff n'efface aucun fichier de test `*_test.*`, `tests/`, `test/` sauf mention explicite dans le
  ticket), puis pousse avec le token bot, puis relit le SHA distant via l'API GitHub.
- Jamais de force-push : un push refusé (non fast-forward) → BLOCKED.

## 6. Moteur de jobs

- Dequeue : `UPDATE jobs SET status='leased', lease_owner=$1, lease_expires_at=now()+$2, attempts=attempts+1
  WHERE id = (SELECT id FROM jobs WHERE status='queued' AND run_after<=now() AND NOT paused
  ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`.
- Heartbeat toutes les `lease/3` ; perte du lease → le handler est annulé via `context`.
- Recovery (toutes les 30 s) : jobs `leased` avec lease expiré → `queued` (si `attempts < max_attempts`)
  sinon `failed` + sujet BLOCKED. Un job qui a déjà un `hermes_run_id` **ne resoumet pas** : il se
  rattache au run existant (poll du statut). La clé d'idempotence Hermes = `aos-job-<job_id>-a<attempt du sujet>`.
- `dispatch_paused` (settings) : aucune nouvelle prise de job ; les runs en cours continuent.
- Stop d'un run : `POST /p/<profil>/v1/runs/<id>/stop`, job `cancelled`, sujet BLOCKED (raison « arrêté »).
- Kinds : `po_spec`, `tech_design`, `open_issues`, `develop`, `review`, `qa`, `publish_pr`,
  `execute_approval`, `sync_agents`.
- Un seul job `develop|review|qa` actif par projet (sérialisation simple V1).

## 7. Contrat Hermes (vérifié en live sur v0.21.5)

- `POST {AOS_HERMES_URL}/p/<profil>/v1/runs`, header `Authorization: Bearer <clé du profil>`,
  `Idempotency-Key`, body `{"input": "...", "instructions": "..."}` → `202 {"run_id","status":"started","replayed":bool}`.
  Même clé + payload différent → `409 idempotency_key_conflict` (traité comme erreur `internal`, jamais retenté avec une autre clé automatiquement).
- `GET /p/<profil>/v1/runs/<id>` → `{"status": "started|running|completed|failed|...", "output", "usage":{"input_tokens","output_tokens","total_tokens"}, "runtime":{"provider","model"}, "completed","partial","interrupted"}`. `404 run_not_found` → run `lost`.
- `GET /p/<profil>/v1/runs/<id>/events` → SSE, lignes `data: {"event": "...", "seq": n, ...}` ; événements
  observés : `message.delta`, `reasoning.available`, `tool.*`, `run.completed`, `run.failed`. Les deltas
  ne sont pas persistés un par un : on persiste les événements non-delta et on agrège la sortie.
- `POST /p/<profil>/v1/runs/<id>/stop`.
- `GET /p/<profil>/v1/capabilities` (health) ; modèle observé = `runtime.model` du dernier run, et
  `GET /p/<profil>/v1/models` si disponible.
- Le livrable est le **dernier bloc JSON** de `output` (balise ```json … ``` ou objet brut). Budget :
  si `usage.total_tokens > max_tokens_per_run` → erreur `budget` (le livrable est quand même stocké mais rejeté).

Livrables (schémas stricts, clés inconnues refusées) :
- `po_spec` : `{"summary": str, "user_stories": [str], "acceptance_criteria": [str], "open_questions": [str]}`
- `tech_design` : `{"summary": str, "decisions": [{"decision","rationale","rejected_alternatives":[str]}], "risks": [str], "tickets": [{"title","body","acceptance_criteria":[str]}]}` (1..10 tickets)
- `develop` : `{"summary": str, "files_changed": [str], "tests": [{"command","exit_code":int}], "notes": str}`
- `review` : `{"verdict": "approved|changes_requested", "sha": str, "findings": [{"severity":"P0|P1|P2|P3","file","message"}], "summary": str}` — `approved` refusé s'il existe un finding P0/P1.
- `qa` : `{"verdict": "passed|failed", "sha": str, "checks": [{"command","exit_code":int}], "acceptance": [{"criterion","status":"met|not_met","evidence"}], "summary": str}` — `passed` refusé si un check a `exit_code≠0` ou un critère `not_met`.

Le contenu GitHub (issue, commentaires, logs) est passé à l'agent **encapsulé** dans un bloc
`<untrusted_data>…</untrusted_data>` sous des instructions fixes, jamais comme instructions.

## 8. GitHub

- REST avec token bot (fichier `github_token`), `User-Agent: agentic-os`.
- Issue : corps terminé par `<!-- agentic-os:ticket=<key> -->`. Avant création : recherche
  (`GET /search/issues?q=repo:<r>+"agentic-os:ticket=<key>"+in:body`) ; après création : relecture
  (numéro, URL, présence du marqueur).
- Push : `git push https://x-access-token@github.com/<r>.git <branch>` avec le token fourni via
  `GIT_ASKPASS` (script temporaire 0700 qui lit le fichier) ; jamais dans l'URL ni dans les logs.
  Puis `GET /repos/<r>/branches/<branch>` → `commit.sha == head_sha`.
- PR : `POST /repos/<r>/pulls` `draft:true`, idempotent (si une PR ouverte existe pour la head, on la
  réutilise), relecture `number/head.sha/base.ref/draft`. Corps : résumé, décisions, verdicts
  Reviewer/QA, critères ↔ preuves, risques, `Closes #<issue>`.
- CI : `GET /repos/<r>/commits/<sha>/check-runs` + `/status` ; `success` seulement si chaque check requis
  du projet est `completed/success` ; un check requis terminé ≠ success → `failure` ; absent/en cours → `pending`.
- Webhook `POST /webhooks/github` : HMAC `X-Hub-Signature-256` vérifié sur le corps brut (comparaison
  constante) avant tout parsing ; dédup `X-GitHub-Delivery` ; événements `pull_request`, `check_run`,
  `check_suite`, `status`. Poller de secours toutes les 60 s pour les tickets PR_OPENED/HUMAN_MERGE.

## 9. API HTTP (JSON, préfixe `/api`)

Authentification : `POST /api/auth/login {login,password}` → cookie `aos_session` (HttpOnly, SameSite=Strict,
Path=/, Secure si HTTPS) + `{"csrf_token"}`. Toute requête non-GET exige l'en-tête `X-CSRF-Token`.
`POST /api/auth/logout`, `GET /api/auth/me` → `{login, csrf_token}`. Rate-limit login (5/min/IP).
Erreurs : `{"error": {"code": "snake_case", "message": "texte FR"}}` avec 400/401/403/404/409/422/503.
Listes : `?limit=` (≤200, défaut 50) `&cursor=` → `{"items": [...], "next_cursor": str|null}`.
Dates : ISO-8601 UTC ; l'UI formate en Europe/Paris.

| Méthode | Route | Description |
|---|---|---|
| GET | `/api/overview` | `{pending_approvals:int, blocked:{features,tickets}, active_runs:int, tokens_today:int, tokens_7d:[{day,total}], dispatch_paused:bool, alerts:[{level,message,link}], recent_activity:[audit]}` |
| GET/POST | `/api/projects` | liste / création `{slug,name,repository,base_branch,required_checks[],mode,test_command}` |
| GET/PATCH | `/api/projects/{id}` | détail / mise à jour ; activer un projet `product` exige `{"enabled":true,"confirm":"<slug>"}` |
| GET | `/api/agents` | liste avec état de sync |
| PATCH | `/api/agents/{role}` | `{desired_model?, max_tokens_per_run?, enabled?}` |
| POST | `/api/agents/sync` | crée un job `sync_agents` |
| GET/POST | `/api/features` | liste (`?state=&project_id=`) / création `{project_id,title,description}` |
| GET | `/api/features/{id}` | détail : feature + spec + design + tickets + approvals + runs + timeline (audit) |
| POST | `/api/features/{id}/actions` | `{"action":"start_spec|cancel|retry"}` |
| GET | `/api/tickets/{id}` | détail ticket + runs + approvals + timeline |
| POST | `/api/tickets/{id}/actions` | `{"action":"cancel|retry"}` |
| GET | `/api/pipeline` | `{columns:[{state,label,items:[{type:"feature"|"ticket",id,title,project,state,updated_at,attention:bool}]}]}` |
| GET | `/api/runs` | liste filtrable `?status=&role=&project_id=` |
| GET | `/api/runs/{id}` | détail + événements persistés + sortie + livrable |
| POST | `/api/runs/{id}/stop` | arrêt |
| GET | `/api/approvals` | `?status=pending` |
| GET | `/api/approvals/{id}` | détail (title, effect, details) |
| POST | `/api/approvals/{id}/decision` | `{"decision":"approved|rejected","comment":str}` (comment requis si rejected) ; décision et job durable `execute_approval` créés atomiquement ; 409 si déjà décidée |
| GET | `/api/audit` | `?subject_type=&subject_id=&actor=` |
| GET | `/api/system/health` | `{database, hermes, github, telegram, profiles:[{profile,status}], workers, schema_version, version, revision}` |
| POST | `/api/system/dispatch` | `{"paused":bool}` |
| GET | `/api/events` | SSE : `event: <type>\ndata: {json}` types `job.updated`, `run.updated`, `run.event`, `approval.updated`, `feature.updated`, `ticket.updated`, `overview.invalidate` ; heartbeat `: ping` toutes les 15 s. Payload `{id}` sauf `run.event` : `{id, run_id, seq, event, data, created_at}` (même forme que `events[]` de `GET /api/runs/{id}`) |
| GET | `/healthz` | 200 si le process vit (sans auth) |
| GET | `/readyz` | 200 si DB ok et migrations à jour (sans auth) |
| POST | `/webhooks/github` | HMAC, sans session |

Les réponses ne contiennent jamais : secrets, clés d'idempotence, lease owner, chemins hôte complets
(les worktrees sont affichés relativement à `AOS_WORKSPACE_ROOT`).

## 10. Sécurité

- CSP stricte (`default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'`), `X-Content-Type-Options`, `Referrer-Policy: no-referrer`.
- Tous les `exec.Command` utilisent des binaires absolus (`/usr/bin/git`), argv explicites, env minimal,
  timeout via `context`, sortie bornée.
- Les chemins de worktree sont calculés côté serveur (`<root>/<project slug>/<ticket key>`), validés
  (pas de `..`, sous la racine).

## 11. Exploitation

- `deploy/compose.yaml` : services `db` (postgres:16-alpine, volume `aos-pgdata`, `127.0.0.1:55433`),
  `migrate` (one-shot `agentic-os migrate`), `app` (`agentic-os serve`, host network, restart unless-stopped).
- `deploy/Dockerfile` multi-stage : node:22-alpine (build web) → golang:1.23-alpine (build, `-trimpath`,
  `CGO_ENABLED=0`, ldflags version/revision) → alpine:3.20 + git + ca-certificates, user 1000.
- `Makefile` : `test`, `test-integration` (Postgres jetable via Docker), `lint` (`go vet`, `gofmt -l`),
  `web-build`, `web-test`, `build`, `up`, `down`, `backup` / `restore` / `ops-test` (voir sauvegarde-restauration.md).
- `agentic-os doctor` : vérifie DB, migrations, Hermes par profil, GitHub (token, accès dépôts), Telegram,
  racine workspace inscriptible, `git` présent ; sortie JSON + code retour ≠ 0 si un point bloque.
- CI GitHub Actions `.github/workflows/tests.yml`, job **`tests`** : `go vet`, `go test ./...` avec
  service Postgres, `npm ci && npm run build && npm test` dans `web/`.

## 12. Précisions d'implémentation V1 (ajouts au contrat)

Ajouts introduits par l'implémentation du backend ; ils complètent les sections précédentes sans les
contredire.

**Machines à états**
- Ticket : état initial `PLANNED` (créé par le job `open_issues`), événement `ready` PLANNED → READY_FOR_DEV
  lors de l'approbation `dev_gate`.
- Feature : événement générique `fail` depuis SPEC, TECH_DESIGN, ISSUES_OPENED → BLOCKED (échec de job) ;
  `dev_gate` rejeté → CANCELLED (tickets annulés) ; READY_FOR_DEV → DONE si tous les tickets sont terminaux.
- Ticket : `cancel` depuis tout état non terminal ; `fail` aussi depuis READY_FOR_DEV et PR_OPENED ;
  `publish_pr` rejeté → CANCELLED ; `rework` approuvé avec `attempt ≥ AOS_MAX_REWORK` → `rework_exhausted`
  (CHANGES_REQUESTED → BLOCKED, approbation `execution_failed`) ; `retry` vers CHANGES_REQUESTED recrée
  l'approbation `rework`.
- `attempt` = nombre de reworks effectués (0 au premier développement). Clé Hermes des jobs de feature : `a0`.
- L'état avant blocage est conservé dans `previous_state` (features, tickets) pour `retry`.

**Schéma** : colonnes ajoutées `features.previous_state`, `tickets.previous_state`, `tickets.seq`,
`jobs.project_id` (sérialisation develop|review|qa par index unique partiel), `approvals.decision`
(décision conservée après exécution) ; `jobs.subject_type` accepte `system` (sync_agents).
Agents initialisés avec les profils `aos-po`, `aos-tech-lead`, `aos-developer`, `aos-reviewer`, `aos-qa`.

**Approbations** : la décision humaine et le job `execute_approval` sont persistés dans la même transaction.
Le worker verrouille le sujet puis l'approbation, applique l'effet de façon idempotente et renseigne
`executed|execution_failed`. Les approbations de design, de mise en développement et de publication figent
`project_id`, dépôt, branche de base et checks requis ; toute divergence refuse l'effet. L'API refuse aussi de
modifier ces champs tant qu'une feature du projet est non terminale. Une annulation expire les approbations
encore actionnables et annule leurs jobs d'exécution.

**SSE `run.event`** : le NOTIFY PostgreSQL ne transporte que `run_id`/`seq` (limite 8000 octets) ; l'écouteur relit la
ligne `run_events` persistée avant diffusion. Après une reconnexion, l'UI recharge le détail et déduplique par `seq`.

**Fiabilité du moteur** :
- Recovery : un job épuisé est marqué `failed` et son sujet bloqué dans la même transaction (un job à la fois,
  `FOR UPDATE SKIP LOCKED`) ; en cas d'erreur, le job reste `leased` expiré et la recovery suivante recommence.
- Heartbeat : une erreur PostgreSQL n'est plus ignorée ; sans heartbeat réussi depuis 2/3 du lease, le worker
  annule le handler avant l'expiration (le job est repris par la recovery).
- Fencing : le lease est revérifié (et prolongé) juste avant chaque effet externe — soumission Hermes,
  création d'issue, push Git, création de PR ; lease perdu → effet annulé, aucune finalisation.
- Push : la branche distante doit être absente (premier push), égale au `head_sha` vérifié, ou égale au HEAD
  local (push d'une tentative interrompue) ; toute autre valeur (push externe) → ticket BLOCKED, sans push.

**Arrêt des runs** (migration 0003) : un arrêt n'est jamais présumé. Le run passe en `stopping`
(`stop_requested_at`, `stop_attempts`, `stop_error`), le job est annulé et le sujet bloqué atomiquement ; puis
`POST …/stop` est envoyé et le statut est relu chez Hermes. Seul un statut terminal relu finalise le run
(`stopped`, ou l'issue réelle si le run s'est terminé entre-temps ; 404 → `lost`). Sinon la réconciliation
(30 s) retente ; alerte sur la vue d'ensemble après 3 tentatives. `POST /api/runs/{id}/stop` renvoie le run
(`stopping` si non confirmé) au lieu d'un 503.

**Webhooks retraitables** (migration 0003) : après vérification HMAC, le corps est conservé dans
`github_deliveries.payload` ; statuts `received → processing → processed | failed`. Échec de réconciliation →
`failed` avec `next_attempt_at` (backoff exponentiel, 8 tentatives max), repris toutes les 30 s ; une
redélivrance GitHub (même `X-GitHub-Delivery`) d'une livraison non traitée est retraitée immédiatement ; un
traitement abandonné (> 5 min en `processing`) est repris. Réponses : 200 `processed|duplicate`, 202
`retry_scheduled`. Corps invalide : `failed` sans reprise. Les livraisons antérieures à 0003 n'ont pas de corps
et ne sont pas retraitables.

**Validations de saisie** : règles uniques côté API (`internal/domain/validation.go`) et UI
(`web/src/lib/validation.ts`), verrouillées par la fixture `web/src/test/fixtures/validation-cases.json`
testée des deux côtés. Longueurs en caractères (pas en octets) : slug 1–40, nom de projet 200, commande de
test 500, 20 checks requis de 200 caractères, titre de feature 200, description **obligatoire** 20 000,
budget 1 000–50 000 000 tokens, modèle vide (aucun imposé) ou `[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}`.

**Listes et troncature** : l'UI lit toutes les pages des projets et approbations (curseur) au lieu de
tronquer à 200. Les détails bornés le signalent explicitement et gardent les éléments **les plus récents** :
`events_truncated` (2 000 derniers événements d'un run), `timeline_truncated` (1 000 dernières entrées
d'audit d'une feature/d'un ticket, lien vers `/api/audit`). Les approbations d'un sujet ne sont pas bornées.

**Agents** : sans `desired_model`, un agent observé est `SYNCED` (plus de faux `DRIFT_DETECTED`) ; un PATCH ne
masque plus un état `UNREACHABLE`.

**Fuseau** : `AOS_PUBLIC_TZ` est supprimée ; l'affichage est toujours en Europe/Paris (UI), les API restent en UTC.

**QA** : le livrable doit contenir chaque critère d'acceptation persistant exactement une fois ; critères omis,
inventés ou dupliqués sont refusés. Une PR réutilisée doit rester ouverte et draft, pas seulement une PR créée
pendant le job courant.

**Git** : copie bare `<root>/<slug>/_repo.git` ; worktree de dev `<root>/<slug>/<key>` sur la branche
`aos/<key>` ; reviewer/QA en worktrees détachés `<key>-review`, `<key>-qa`. Toutes les commandes git
passent `-c core.hooksPath=/dev/null -c credential.helper=`. La propreté inclut les fichiers non suivis
(les fichiers ignorés par `.gitignore` ne sont pas comptés). Une suppression de test n'est autorisée que si
le chemin supprimé figure tel quel dans le corps du ticket.

**Configuration** (en plus du §3) : `AOS_ADMIN_LOGIN` (défaut `admin`), `AOS_GIT_BINARY` (`/usr/bin/git`),
`AOS_GITHUB_API_URL`, `AOS_GITHUB_WEB_URL`, `AOS_TELEGRAM_API_URL` (tests), `AOS_COOKIE_SECURE=1`,
`AOS_ALLOW_NON_LOOPBACK=1` (sinon `serve` refuse une adresse non loopback). Secrets de déploiement
supplémentaires : `database_url`, `postgres_password`.

**API** : `PATCH /api/agents/{role}` accepte aussi `hermes_profile`. Les projets sont créés désactivés
(`enabled` via PATCH ; confirmation exigée seulement pour `product`). Codes supplémentaires : 429
`too_many_attempts` (rate-limit login), 405, 500 `internal`. Le webhook renvoie 503 si
`github_webhook_secret` est absent. Les détails feature/ticket incluent `jobs` (sans clé d'idempotence ni
lease owner) et le ticket expose `worktree` relatif (`<slug>/<key>`).
