# Changelog

Format : [Keep a Changelog](https://keepachangelog.com/fr/1.1.0/) ; versions : [SemVer](https://semver.org/lang/fr/).

## [Non publié] — 1.0.0

Première version publiée. Le périmètre est figé dans `docs/produit/fonctionnalites.md` et le contrat
dans `docs/technique/architecture.md`.

### Ajouté
- Control plane Go (chi, pgx, PostgreSQL) et interface React embarquée : projets, features, tickets,
  runs, approbations, agents, audit, santé.
- Orchestration des agents Hermes PO → Tech Lead → Développeur → Reviewer → QA, jusqu'à la PR draft ;
  merge toujours humain.
- Moteur de jobs à leases PostgreSQL, idempotence, reprise après crash, backoff.
- Approbations humaines durables : effet exécuté par un job idempotent et récupérable, paramètres
  approuvés figés et revérifiés à l'exécution (#2).
- Flux live des runs (SSE) avec reprise des événements manqués (#2, #4).
- Webhooks GitHub signés (HMAC), conservés et retraités en cas d'échec ; poller de secours (#3).
- Notifications Telegram ; `agentic-os doctor` ; déploiement Docker Compose mono-hôte.
- Sauvegarde vérifiée, restauration, mise à jour et retour arrière scriptés et testés (#6).
- Tests de bout en bout navigateur, responsive et accessibilité (PR e2e).
- Workflow `security` (govulncheck, npm audit) et Dependabot (#5).
- Guide d'utilisation et runbook d'exploitation (#7).

### Sécurité
- Secrets lus uniquement depuis des fichiers 0600 ; jamais dans les logs, l'argv, les URL ou
  l'environnement ; token Git transmis par `GIT_ASKPASS` éphémère.
- QA bijective sur les critères d'acceptation ; PR réutilisée seulement si ouverte, draft et avec
  head/base exacts (#2).
- Fencing des leases avant chaque effet externe ; détection des pushs externes ; jamais de force-push (#3).
- Arrêt d'un run jamais présumé : seul le statut relu chez Hermes le confirme (#3).
- Toolchain Go 1.27 supportée, dépendances sans vulnérabilité connue, images et actions épinglées (#5).

### Corrigé
- Validations identiques entre l'interface et l'API (fixture partagée) ; listes paginées sans
  troncature silencieuse ; faux `DRIFT_DETECTED` sans modèle souhaité (#4).
