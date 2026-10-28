# Changelog

Format : [Keep a Changelog](https://keepachangelog.com/fr/1.1.0/) ; versions : [SemVer](https://semver.org/lang/fr/).

## [Non publié]

### Ajouté
- Image Docker des agents par projet (`projects/<slug>/`) : outillage de la CI du projet et caches
  de dépendances pré-remplis, pour vérifier sans réseau. Premier projet : FraisZen (PHP 8.5,
  Composer, Node 22 ; Pest, PHP-CS-Fixer, PHPStan, Psalm, typecheck rejoués hors ligne).
- `Taskfile.yml` : `task project:setup|image:build|image:verify|image:check|image:refresh|profiles`.

## [1.2.0] — 2026-10-28

Réduction et lisibilité de la consommation de tokens des agents.

### Modifié
- Consignes des agents plus économes en contexte : skills du rôle seulement, lectures et recherches
  ciblées, sorties bornées, commandes regroupées, base de comparaison `origin/<base>` (la branche
  locale peut être périmée et faire relire des changements déjà intégrés) (#31).
- Tokens : entrée nouvelle, relue du cache et sortie affichées séparément (détail d'un run, liste des
  runs, vue d'ensemble, histogramme 7 jours) ; migration 0004 avec reprise des runs existants (#30).

## [1.1.0] — 2026-10-27

### Ajouté
- Graphe Git par projet (Projets → Graphe Git) : branches GitFlow actives, merges, tags, liens vers
  PR, commits et tickets ; filtres par période et type de branche ; branches mergées masquées par
  défaut (merge, squash). Lecture seule (#27).

### Modifié
- CI : un seul run par check sur les PR ; les push ne déclenchent les suites que sur `main` et
  `develop` ; `tests` lançable à la main (#25).

## [1.0.1] — 2026-10-27

Retours de la recette réelle de la 1.0.0.

### Modifié
- Corps des PR courts et centrés sur le ticket : résumé du développeur borné, verdicts, couverture
  des critères, au plus 5 commandes, preuves repliées ; le design et les risques de la feature ne
  sont plus recopiés dans chaque PR (#20).
- Pipeline : seules les étapes non vides sont affichées par défaut ; bouton « Afficher les étapes
  vides », choix mémorisé (#21).
- Tech Lead : découpage minimal (une petite feature = 1 ticket, pas de ticket d'analyse sans code,
  tests dans le ticket du code) ; 5 tickets au maximum par feature au lieu de 10 (#22).

## [1.0.0] — 2026-10-25

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
- Tests de bout en bout navigateur, responsive et accessibilité (#9).
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
- Défauts trouvés par la suite e2e : interface encore affichée après déconnexion, rejet exécuté
  présenté comme « Exécutée », défilement horizontal mobile, contrastes en thème clair, zones
  défilantes inaccessibles au clavier, livrable d'un run terminé non affiché sans rechargement (#9).
- Trouvés par la recette réelle (vrai Hermes, profils isolés) : push Git impossible depuis le conteneur
  (script d'authentification dans un `/tmp` non exécutable) et tickets d'une feature développés en
  parallèle au lieu de séquentiellement par projet (#18).
