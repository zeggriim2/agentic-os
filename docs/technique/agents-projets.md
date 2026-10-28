# Agents par projet : image Docker et profils Hermes

Le Développeur, le Reviewer et la QA exécutent du code dans un conteneur Docker **sans réseau** et
**sans secret**. Pour un vrai projet (PHP, Node…), ce conteneur doit contenir l'outillage et les
dépendances du projet. Chaque projet a donc :

- une **image** `agentic-os-agent-<slug>` : outillage de la CI du projet et caches Composer/npm
  pré-remplis depuis ses fichiers lock (le réseau ne sert qu'au `docker build`) ;
- des **profils Hermes** `aos-<slug>-developer|reviewer|qa` : clonés des profils par défaut
  (consignes, skills, outils, isolation), avec l'image du projet et **seulement** `workspaces/<slug>`
  monté (lecture seule pour Reviewer et QA) ;
- le réglage `agent_profiles` du projet dans Agentic OS, qui confie ces rôles à ces profils.

Le PO et le Tech Lead n'exécutent rien : ils restent communs à tous les projets.

## Fichiers d'un projet : `projects/<slug>/`

| Fichier | Rôle |
|---|---|
| `project.env` | dépôt, branche de base, fichiers lock, commande de vérification, ressources |
| `Dockerfile` | image des agents ; reçoit le dépôt en contexte nommé `project` (fichiers lock seulement) |
| `aos-setup` | dans le worktree, sans réseau : `.env` et clés de test (ignorés par git), `composer install`, `npm ci` depuis les caches |
| `aos-ci` | rejoue les jobs de la CI du projet (FraisZen : « Tests Backend » et « Code Quality » ; « Lint » reste dans GitHub) |

## Commandes (`task --list`)

```bash
task project:setup PROJECT=fraiszen          # build + vérification hors ligne + profils
task project:image:build PROJECT=fraiszen    # image taguée par le hash des fichiers lock (+ :current)
task project:image:verify PROJECT=fraiszen   # CI du projet dans l'image, --network none, UID de l'hôte
task project:image:check PROJECT=fraiszen    # à jour / à reconstruire (exit 1)
task project:image:refresh PROJECT=fraiszen  # reconstruit et revérifie seulement si un lock a changé
task project:profiles PROJECT=fraiszen       # crée ou met à jour les profils aos-<slug>-*
```

`task` s'installe avec `go install github.com/go-task/task/v3/cmd/task@v3.45.4`.

## Quand reconstruire l'image

Quand `composer.lock` ou `frontend/package-lock.json` change sur la branche de base
(`task project:image:refresh`). Les conteneurs d'agents ne sont pas persistants : le run suivant
utilise la nouvelle image `:current`, sans redémarrage.

Un ticket qui **ajoute** une dépendance ne peut pas l'installer (pas de réseau) : `aos-setup` échoue
avec un message explicite, l'agent le signale dans son livrable. Reconstruire l'image depuis la
branche du ticket n'est pas prévu : merger d'abord l'ajout de dépendance, ou relancer le ticket
après reconstruction.

## Sécurité

- Pendant les runs : `--network none`, UID de l'hôte, aucun secret, aucune variable d'environnement
  de l'hôte ; `COMPOSER_DISABLE_NETWORK=1` et `npm_config_offline=true` dans l'image.
- Le `.env` créé par `aos-setup` ne contient que des valeurs factices (comme la CI).
- Le build récupère le dépôt avec le token d'Agentic OS lu depuis son fichier par un credential
  helper : il n'entre ni dans l'argv, ni dans l'image (seuls les fichiers lock sont copiés).
- Les profils d'un projet ne voient que les worktrees de ce projet.

## Nouveau projet

1. Copier `projects/fraiszen/` vers `projects/<slug>/`, adapter `project.env`, le `Dockerfile`
   (versions, extensions) et `aos-ci` (commandes de la CI du projet).
2. `task project:setup PROJECT=<slug>` : la vérification doit passer avant d'aller plus loin.
3. Créer le projet dans Agentic OS avec `agent_profiles` = les trois profils `aos-<slug>-*` et la
   commande de test `aos-setup && aos-ci all`.
