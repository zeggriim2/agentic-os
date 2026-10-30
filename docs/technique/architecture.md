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

Stack : Go 1.27, `github.com/go-chi/chi/v5`, `github.com/jackc/pgx/v5`, logs `log/slog` JSON.
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
  `version int` (verrou optimiste), `integration_mode` (`direct|feature_branch`, défaut = celui du projet),
  `feature_branch`, `feature_pr_number`, `feature_pr_url` (migration 0008).
- `tickets` : `feature_id`, `project_id`, `key` unique (`<slug>-F<feature>-T<n>`), `title`, `body`,
  `acceptance jsonb`, `state`, `state_reason`, `issue_number`, `issue_url`, `branch`, `head_sha`,
  `attempt int`, `pr_number`, `pr_url`, `ci_state` (`unverified|pending|success|failure`), `version`.
- `jobs` : `kind`, `subject_type` (`feature|ticket|approval`), `subject_id`, `status`
  (`queued|leased|succeeded|failed|cancelled`), `attempts`, `max_attempts`, `run_after`,
  `lease_owner`, `lease_expires_at`, `idempotency_key` unique, `last_error`, `error_class`
  (`timeout|budget|policy|provider|invalid_output|external|internal`), `payload jsonb`.
- `runs` : `job_id`, `agent_role`, `hermes_profile`, `hermes_run_id` unique nullable, `status`
  (`submitted|running|completed|failed|stopped|lost`), `input_tokens`, `output_tokens`, `total_tokens`, `cache_read_tokens`, `cache_write_tokens` (0004),
  `model`, `provider`, `output text` (borné 256 KiB), `artifact jsonb`, `error`, `started_at`, `finished_at`,
  `requested_model`, `requested_provider`, `model_fallback bool` (0007 : surcharge demandée et repli).
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
| IN_DEV | tous tickets DONE/CANCELLED | DONE | mode `direct`, ou aucun ticket DONE |
| READY_FOR_DEV / IN_DEV | `tickets_integrated` : tous terminaux, ≥ 1 DONE, mode `feature_branch` | ACCEPTANCE | approval `publish_feature_pr` |
| ACCEPTANCE | PR finale mergée (`feature_pr_merged`) | DONE | |
| ACCEPTANCE | `fail` : PR finale refusée, fermée sans merge, ou job en échec | BLOCKED | `retry` → ACCEPTANCE, nouvelle approbation si aucune PR ouverte |
| * (non terminal) | `cancel` (user) | CANCELLED | jobs annulés, runs stoppés |
| BLOCKED | `retry` (user) | état précédent | rejoue le job |

`NEEDS_CLARIFICATION` : si le livrable PO contient `open_questions` non vides, la feature va en
SPEC_REVIEW quand même ; l'approbation affiche les questions et le rejet avec commentaire sert de réponse.

### Ticket
| Depuis | Événement | Vers | Effet |
|---|---|---|---|
| READY_FOR_DEV | job `develop` démarre | DEV | worktree + run developer |
| DEV | `dev_pushed` (sha vérifié) | REVIEW | job `review` |
| DEV (reprise) | aucun commit, `files_changed: []`, branche distante = `head_sha` | REVIEW | job `review` sur le même SHA, sans push (depuis 1.6.1) |
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
  `Idempotency-Key`, body `{"input": "...", "instructions": "...", "model"?: "...", "provider"?: "..."}` → `202 {"run_id","status":"started","replayed":bool}`.
  `model`/`provider` (facultatifs, omis quand vides) imposent le modèle servi ; un fournisseur non
  authentifié sur l'hôte fait échouer le run (`failed`, message de résolution du fournisseur), Hermes ne
  replie pas lui-même.
  Même clé + payload différent → `409 idempotency_key_conflict` (traité comme erreur `internal`, jamais retenté avec une autre clé automatiquement).
- `GET /p/<profil>/v1/runs/<id>` → `{"status": "started|running|completed|failed|...", "output", "usage":{"input_tokens","output_tokens","total_tokens","cache_read_tokens","cache_write_tokens"}, "runtime":{"provider","model"}, "requested"?:{"provider","model"}, "route_source": "global|raw_request|model_routes", "completed","partial","interrupted"}`. `404 run_not_found` → run `lost`.
  `runtime` est le modèle **réellement servi** ; `requested` n'apparaît qu'après une surcharge.
- `GET /p/<profil>/api/model/options` (même clé Bearer) → `{model, provider, providers:[{slug, name,
  is_current, authenticated, models:[...], …}]}` : catalogue des fournisseurs de l'**hôte** Hermes
  (partagé par tous les profils) ; seuls les fournisseurs `authenticated: true` peuvent servir un run.
  `GET /v1/models` ne renvoie que l'alias du profil : inutilisable pour lister les LLM.
- `GET /p/<profil>/v1/runs/<id>/events` → SSE, lignes `data: {"event": "...", "seq": n, ...}` ; événements
  observés : `message.delta`, `reasoning.available`, `tool.*`, `run.completed`, `run.failed`. Les deltas
  ne sont pas persistés un par un : on persiste les événements non-delta et on agrège la sortie.
- `POST /p/<profil>/v1/runs/<id>/stop`.
- `GET /p/<profil>/v1/capabilities` (health) ; modèle observé = `runtime.model` du dernier run, et
  `GET /p/<profil>/v1/models` si disponible.
- Le livrable est le **dernier bloc JSON** de `output` (balise ```json … ``` ou objet brut). Budget :
  si les tokens nouveaux (`input_tokens - cache_read_tokens + output_tokens`) dépassent `max_tokens_per_run` → erreur `budget` (le livrable est quand même stocké mais rejeté). Depuis 1.5.1 ; avant, le total brut (cache compris) était compté.

Livrables (schémas stricts, clés inconnues refusées) :
- `po_spec` : `{"summary": str, "user_stories": [str], "acceptance_criteria": [str], "open_questions": [str]}`
- `tech_design` : `{"summary": str, "decisions": [{"decision","rationale","rejected_alternatives":[str]}], "risks": [str], "tickets": [{"title","body","acceptance_criteria":[str]}]}` (1..5 tickets : développement séquentiel, une PR par ticket ; consignes de découpage minimal dans le prompt)
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
  un credential helper shell (`-c credential.helper=!f(){…}`) qui lit le fichier à la demande — aucun
  fichier temporaire exécutable (compatible `/tmp` noexec) ; jamais dans l'URL, l'argv ni les logs.
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
| GET | `/api/overview` | `{pending_approvals:int, blocked:{features,tickets}, active_runs:int, tokens_today:int, tokens_today_split:{fresh_input,cache_read,output}, tokens_7d:[{day,total,fresh_input,cache_read,output}], dispatch_paused:bool, alerts:[{level,message,link}], recent_activity:[audit]}` |
| GET/POST | `/api/projects` | liste / création `{slug,name,repository,base_branch,required_checks[],mode,test_command}` |
| GET/PATCH | `/api/projects/{id}` | détail / mise à jour ; activer un projet `product` exige `{"enabled":true,"confirm":"<slug>"}` |
| GET | `/api/projects/{id}/git-graph` | graphe Git en lecture seule : `?days=1..365` (30), `kinds=main,develop,release,hotfix,feature,other` (tous), `merged=0\|1` (0), `refresh=1` ; réponse `{project,days,since,truncated,merged,hidden_merged,refs[],tags[],commits[]}` (§12) |
| GET | `/api/agents` | liste avec état de sync |
| PATCH | `/api/agents/{role}` | `{desired_model?, max_tokens_per_run?, enabled?}` |
| POST | `/api/agents/sync` | crée un job `sync_agents` |
| GET/POST | `/api/features` | liste (`?state=&project_id=`) / création `{project_id,title,description}` |
| GET | `/api/features/{id}` | détail : feature + spec + design + tickets + approvals + runs + timeline (audit) |
| POST | `/api/features/{id}/actions` | `{"action":"start_spec|cancel|retry"}` |
| POST | `/api/features/{id}/integration` | `{"mode":"direct|feature_branch"}` ; 409 `integration_locked` si un ticket est mergé, feature en recette ou terminée (depuis 1.7.0) |
| GET | `/api/tickets/{id}` | détail ticket + runs + approvals + timeline |
| POST | `/api/tickets/{id}/actions` | `{"action":"cancel|retry"}` |
| GET | `/api/pipeline` | `{features:{columns:[…]}, tickets:{columns:[…]}, projects:[{id,slug}]}` ; colonne `{state,label,items:[{type,id,title,project,project_id,state,updated_at,attention}]}` ; feature + `progress:{total,done,current:{key,state}\|null}` (tickets annulés exclus) ; ticket + `key,feature_id,feature_title` (tickets PLANNED masqués). Depuis 1.6.0 (avant : une seule liste `columns`). |
| GET | `/api/hermes/models` | `{items:[{provider,name,is_current,models:[...]}]}` : fournisseurs **authentifiés** du catalogue Hermes (profil du PO, cache 60 s), aucun champ secret ; `503 hermes_unreachable` si Hermes ne répond pas |
| GET | `/api/runs` | liste filtrable `?status=&role=&project_id=` ; chaque run porte `requested_model`, `requested_provider`, `model_fallback` |
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
- `deploy/Dockerfile` multi-stage : node:22-alpine (build web) → golang:1.27-alpine (build, `-trimpath`,
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

**Développement séquentiel par projet** : au feu vert, tous les tickets passent `READY_FOR_DEV` mais
un seul job `develop` est créé. Le suivant (feature la plus ancienne, puis ordre du plan) n'est lancé
que lorsque plus aucun ticket du projet n'est actif — le précédent est mergé (`DONE`) ou annulé ; un
ticket `BLOCKED` garde la main. Chaque ticket part donc de la base à jour, avec les changements des
tickets précédents (verrou consultatif par projet, `advanceProject`).

**Authentification Git** : le token est fourni par un credential helper shell passé en `-c` ; aucun
fichier temporaire exécutable n'est créé, ce qui fonctionne dans le conteneur (`/tmp` en noexec,
système de fichiers en lecture seule). Testé par un push authentifié réel (`git http-backend`).

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

### Graphe Git des projets (ajout post-1.0.1)

- Lecture dans une copie bare dédiée `<workspaces>/<slug>/_graph.git` (distincte de `_repo.git` du
  moteur : aucune contention de verrous), mise à jour par `fetch --prune` des branches et des tags avec
  le même credential helper que les push. Lectures sérialisées, cache de 30 s par projet et filtres.
- Branches actives = modifiées dans la fenêtre ; `main`/`develop` toujours présentes. Une branche est
  « mergée » si sa pointe est un ancêtre de `main`/`develop`, ou si la fusionner n'en change pas
  l'arbre (`merge-tree`, squash/rebase) ; masquée par défaut.
- Commits en ordre topologique, attribués à une branche par premiers parents (priorité main, develop,
  release, hotfix, feature, autres) ; 400 commits au plus (`truncated`). PR déduites des messages de
  merge/squash GitHub ; branches `aos/<ticket>` reliées au ticket et à sa PR depuis la base.
- Erreur Git : `502 git_error` générique, détail uniquement dans les journaux.

### Répartition des tokens (0004)

- Hermes compte dans `input_tokens` les tokens relus du cache du fournisseur (`cache_read_tokens`) :
  sur la recette, 77 % du total affiché était de la relecture de cache. Les runs stockent donc
  `cache_read_tokens`/`cache_write_tokens` (borné : `0 ≤ cache_read ≤ input`) ; l'API expose en plus
  `fresh_input_tokens = input_tokens − cache_read_tokens`. La migration reprend les runs existants depuis
  l'usage de leur événement `run.completed`.
- L'interface affiche « tokens nouveaux » (entrée nouvelle + sortie) avec la relecture du cache à part.
  `total_tokens` garde son sens ; depuis 1.5.1 le budget `max_tokens_per_run` porte sur les tokens nouveaux, pas sur ce total.

### Économie de contexte des agents

- Chaque appel d'outil renvoie tout le contexte au modèle : les instructions des rôles outillés (Tech Lead,
  Développeur, Reviewer, QA) imposent des lectures ciblées, des sorties bornées, des commandes regroupées et
  au plus une ouverture par skill du rôle (`contextRules`).
- Comparaison toujours contre `origin/<base>` (`baseRules`) : la branche locale du même nom, dans la copie
  partagée, peut être périmée. Le Reviewer reçoit sa première commande (`--stat` + diff borné à 400 lignes).
- Les instructions ne contiennent aucune donnée propre au run (SHA, worktree) : elles restent identiques
  d'un run à l'autre, ce qui favorise la réutilisation du cache du fournisseur.

### Profils Hermes par projet (migration 0005)

- `projects.agent_profiles` (JSONB, rôle → profil) : le rôle d'un run est confié au profil du projet
  s'il existe, sinon au profil par défaut de la table `agents` (`Project.ProfileFor`). Le run garde le
  profil utilisé (`runs.hermes_profile`) : changer le réglage n'affecte que les runs suivants.
- L'activation, le budget (`max_tokens_per_run`) et le modèle souhaité restent ceux de l'agent du rôle.
- API : `agent_profiles` dans la création et le `PATCH` d'un projet (remplace l'ensemble ; `{}` = défaut).
  Rôles connus et nom de profil validés (`ValidProfile`) ; la clé `secrets/hermes/<profil>.key` doit
  exister, ce que signalent `/api/health` et `doctor`.
- Usage prévu : `aos-<projet>-developer|reviewer|qa`, avec l'image Docker du projet et seulement
  `workspaces/<projet>` monté (voir `docs/technique/agents-projets.md`).
- `GET /api/hermes-profiles` liste les profils dont la clé `secrets/hermes/<profil>.key` existe (aucune
  clé n'est lue), avec le rôle déduit du suffixe (`-developer`, `-tech-lead`…). Le formulaire projet
  s'en sert pour ses listes déroulantes ; création et `PATCH` refusent un profil absent de cette liste
  (`422 unknown_agent_profile`).

### Modèle LLM par agent et par projet (migration 0007)

- `projects.agent_models` (JSONB, rôle → `{"provider", "model"}`, CHECK objet). Résolution à chaque run
  (`Project.ModelFor`) : modèle du projet pour le rôle → sinon `agents.desired_model` (global, sans
  fournisseur) → sinon rien (défaut du profil Hermes). `model`/`provider` ne sont envoyés dans
  `POST /v1/runs` que lorsqu'une valeur est résolue ; la surcharge est figée à la création du run
  (`runs.requested_model`/`requested_provider`) pour que la clé d'idempotence couvre un corps stable.
- API : `agent_models` dans la création et le `PATCH` d'un projet (remplace l'ensemble ; `{}` ou entrée
  vide = défaut). Validation : rôle connu, `ValidProviderName` + `ValidModelName`, **et** présence dans le
  catalogue (`422 invalid_agent_models` sinon) ; Hermes injoignable au moment de l'écriture →
  `503 hermes_unreachable` (jamais d'acceptation aveugle).
- Repli (moteur, `runAgent`) : si le run se termine `failed` immédiatement pour résolution du
  fournisseur (`hermes.IsProviderResolutionFailure` : erreur citant provider/fournisseur/auth, ou échec
  sans aucun token ni sortie), le run est re-soumis **une seule fois** sans surcharge, sous la clé
  dérivée `<clé>-fallback`, rattaché au même `runs` (`model_fallback=true`, nouveau `hermes_run_id`),
  avec une entrée d'audit `run.model_fallback` (acteur `system`, sujet `run`, `after = {requested,
  served, reason}`). Un second échec termine le run en `failed` (pas de boucle). Le run affiche toujours
  le modèle servi (`runtime`) ; l'UI signale « modèle demandé ≠ servi » et le repli (liste et détail).
- UI projet : bloc « Modèles par agent » (un sélecteur par rôle, groupé par fournisseur, « Défaut du
  profil » en tête, valeur enregistrée absente du catalogue affichée « … (absent) ») ; catalogue
  indisponible → message, formulaire toujours utilisable.
- Le faux Hermes des e2e (`e2e/fakes`) sert `api/model/options` (fournisseur `fake-e2e`) et fait échouer
  tout run demandant un autre fournisseur, ce qui exerce le repli.

### Canal Telegram par projet (migration 0006)

- `projects.telegram_chat_id` (vide ou entier signé) et `projects.telegram_thread_id` (0 = aucun sujet).
- Le moteur notifie via `notifyProject` / `notifySubject` (feature, ticket ou approbation → projet) :
  `notify.Destination{ChatID, ThreadID, Label}`. Canal vide → chat par défaut (`secrets/telegram_chat_id`),
  message préfixé `[<nom du projet>]`, sujet ignoré.
- Secrets : `telegram_bot_token` (obligatoire pour envoyer), `telegram_chat_id` (facultatif). Le bot peut
  être celui de Hermes : `sendMessage` n'interfère pas avec la réception (`getUpdates`) de Hermes.
- `POST /api/projects/{id}/telegram-test` envoie un message de test sur la configuration enregistrée
  (audit `project.telegram_tested`) ; 422 `telegram_not_configured` si le token manque.


### Mode d'intégration par feature (migration 0008, 1.7.0)

- `projects.integration_mode` : défaut proposé aux nouvelles features ; `features.integration_mode` : mode
  effectif (`POST /api/features` accepte `integration_mode`, sinon défaut du projet).
- Cible des tickets : `Feature.TargetBranch(base)` = `feature_branch` en mode `feature_branch` (une fois fixée),
  sinon la base. Le moteur construit pour chaque ticket un projet « effectif » (`ticketProject`) dont
  `BaseBranch` est cette cible : worktree de développement, consignes des agents, approbation et création
  de la PR du ticket, vérification de l'instantané d'approbation. Ce projet n'est jamais persisté.
- Nom de branche : `feature/F<id>-<titre ASCII, 40 car.>` (`domain.FeatureBranchName`), fixé au feu vert
  (ou à la bascule après le feu vert) ; la branche est créée à distance par `gitops.EnsureRemoteBranch`
  (pointe actuelle de `origin/<base>`, jamais de force, branche existante conservée) avant le premier
  `develop` (fence avant l'effet), audit `feature.branch_created` et notification.
- PR de ticket en mode branche de feature : `Refs #N` au lieu de `Closes #N` (GitHub ne ferme pas une
  issue mergée hors de la branche par défaut).
- Fin des tickets (`maybeCompleteFeature` → `completeFeature`) : ACCEPTANCE + approval
  `publish_feature_pr` (instantané projet, `head` = branche de feature, issues) ; approuvée → job
  `publish_feature_pr` : `EnsureDraftPR(branche → base)`, relecture head/base/draft/SHA, stockage
  `feature_pr_number/url`, audit `feature.pr_published`. Refusée → BLOCKED.
- Observation : webhook `pull_request` (`FeaturesByPR` par numéro ou branche) et poller `PollPRs`
  (`ListFeaturesAwaitingPR`) → `ReconcileFeaturePR` : mergée → DONE ; fermée → PR oubliée + BLOCKED.
- Bascule (`Engine.SetFeatureIntegration`) : refusée si feature terminale, en recette (ou bloquée depuis
  la recette) ou si un ticket est DONE. Avant le feu vert : réglage seul + mise à jour de l'effet du
  `dev_gate` en attente. Après : branche fixée (mode `feature_branch`), instantanés des approbations
  `publish_pr` en attente réécrits sur la nouvelle cible, job `switch_integration` : création de la branche
  puis `RetargetPR` (PATCH `base`, relecture) des PR de tickets ouvertes, audit `ticket.pr_retargeted`.
- Pas de rebase automatique de la branche de feature si la base avance : conflits éventuels sur la PR finale.

### Modèle par défaut des profils (migration 0009, 1.8.0)

- `profile_models` : `profile` (PK, `aos-*`), `desired_provider`, `desired_model`, `revision`,
  `state` (`PENDING|APPLIED|FAILED`), `error`, `requested_by/at`, `applied_at`. Une nouvelle demande
  incrémente la révision et repasse en `PENDING` ; le script de l'hôte ne note un résultat que pour la
  révision qu'il a lue (`WHERE revision=$r AND state='PENDING'`).
- `profile_sync_status` (ligne unique) : dernier passage du script ; sans passage depuis 3 min, l'UI
  indique que le timer n'est pas installé ou arrêté.
- `GET /api/profile-models` : profils `aos-*` ayant une clé, modèle courant lu par
  `GET /p/<profil>/api/model/options` (champs `provider`/`model`, cache 30 s, lectures en parallèle,
  15 s max), demande et état, usages (rôle par défaut, surcharges des projets), écarts
  (`mismatches` : projet actif, rôle dont `agent_models` demande un fournisseur différent du défaut du
  profil résolu), état du script.
- `PUT /api/profile-models/{profil}` `{"provider","model"}` : profil `aos-*` ayant une clé (404 sinon,
  422 hors `aos-*`), modèle présent dans le catalogue Hermes (fournisseurs authentifiés), CSRF ; audit
  `profile_model.requested`.
- Application : `deploy/agents/sync-profile-models.sh` (voir `agents-projets.md`), testé par
  `deploy/ops/test-ops.sh` (faux `hermes`, vrai PostgreSQL).
