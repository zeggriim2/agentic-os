# Agentic OS — Fonctionnalités (vision produit)

Agentic OS orchestre des agents Hermes (PO, Tech Lead, Developer, Reviewer, QA) pour transformer
une idée en Pull Request **draft**, sous contrôle humain. Le merge reste toujours humain.

## Périmètre V1 (validé)

1. **Projets** — dépôt GitHub, branche de base, checks CI requis, mode `sandbox` / `product`.
   Un projet `product` ne peut être activé qu'explicitement par un admin.
2. **Agents** — un agent par rôle, lié à un profil Hermes ; modèle souhaité vs observé (dérive),
   budget de tokens par run ; par projet, fournisseur et modèle imposés à chaque rôle (catalogue
   Hermes), avec repli unique et signalé sur le défaut du profil si le fournisseur est refusé.
3. **Workflow Feature** : `IDEA → SPEC → SPEC_REVIEW → TECH_DESIGN → TECH_DESIGN_REVIEW →
   ISSUES_OPENED → HUMAN_GATE → READY_FOR_DEV → IN_DEV → DONE`.
   **Workflow Ticket** : `READY_FOR_DEV → DEV → REVIEW → QA → AWAITING_PR_APPROVAL → PR_OPENED →
   HUMAN_MERGE → DONE`, plus `CHANGES_REQUESTED`, `BLOCKED`, `FAILED`, `CANCELLED`.
   Les transitions sont déterministes ; l'agent produit un livrable, il ne choisit pas l'étape suivante.
4. **Moteur** — file de jobs durable PostgreSQL, leases, heartbeat, recovery, idempotence,
   pause du dispatch / arrêt d'un run.
5. **Hermes** — runs via l'API Hermes (`/p/<profil>/v1/runs`), événements SSE, livrables JSON validés,
   worktree isolé par ticket.
6. **GitHub** — issues avec marqueur anti-doublon, push de branche par le bot, PR **draft** après
   approbation, lecture des checks sur le SHA exact, webhooks HMAC + polling de secours.
   Jamais : merge, force-push, suppression de branche, déploiement.
7. **Quality gates** — Reviewer puis QA sur le même SHA, fail-closed ; prêt seulement si
   Reviewer + QA + CI verts sur ce SHA.
8. **Approbations** — demande → décision → exécution, auditées séparément ; notification Telegram.
9. **Sécurité** — login admin, sessions, CSRF, secrets en fichiers, contenu GitHub non fiable,
   aucune commande libre depuis l'UI, écoute loopback.
10. **Audit & observabilité** — journal append-only, tokens par run/agent/projet, `/healthz`,
    `/readyz`, commande `doctor`.
11. **Interface** — Vue d'ensemble, Pipeline, Feature, Runs (temps réel), Approbations, Agents,
    Projets, Audit, Santé ; thème sombre/clair, responsive, heure de Paris.
12. **Exploitation** — `docker compose up`, migrations versionnées, sauvegarde avant migration, CI.

## V2 (plus tard)

Workflows YAML configurables, approbation depuis Telegram, rôles utilisateurs, synchronisation
complète des profils Hermes, Prometheus, graphiques de coûts, palette ⌘K.

## Ce que l'utilisateur fait lui-même

- Valider la spec, le design, la mise en dev (HUMAN_GATE), chaque rework et chaque publication de PR.
- Merger la PR sur GitHub.
