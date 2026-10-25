# Agentic OS

Control plane Go permettant d’orchestrer des agents Hermes — Product Owner, Tech Lead, Developer, Reviewer et QA — pour transformer une idée en Pull Request draft sous contrôle humain.

## Architecture

- backend Go et API HTTP ;
- PostgreSQL pour l’état, les jobs, les approbations et l’audit ;
- interface React + TypeScript embarquée dans le binaire ;
- intégrations Hermes, GitHub et Telegram ;
- déploiement Docker Compose mono-hôte.

## Documentation

- [Vision produit](docs/produit/fonctionnalites.md)
- [Contrat d’architecture V1](docs/technique/architecture.md)
- [Workflow GitFlow et politique de maintenance](docs/technique/gitflow.md)
- [Interface web](web/README.md)

## Vérifications locales

```bash
make test

cd web
npm ci
npm run lint
npm test
npm run build
```

Les tests d’intégration nécessitent une instance PostgreSQL et la variable `AOS_TEST_DATABASE_URL`. Consulte `deploy/compose.yaml` pour le déploiement et les prérequis de secrets.

## Workflow Git

Le dépôt utilise GitFlow :

- `main` pour les versions publiées ;
- `develop` pour l’intégration ;
- `feature/*`, `fix/*` et `aos/*` pour les changements isolés ;
- `release/*` pour stabiliser une publication ;
- `hotfix/*` pour les corrections urgentes ;
- `support/X.Y` pour maintenir d’anciennes lignes de versions.

Aucun merge ou déploiement n’est effectué automatiquement par Agentic OS : les gates et le merge final restent humains.

## Utilisation et exploitation

- [Guide d’utilisation](docs/produit/guide-utilisateur.md) : parcours d’une feature et d’un ticket, écrans, blocages.
- [Exploitation](docs/technique/exploitation.md) : installation, secrets, isolation des profils Hermes, surveillance, incidents.
- [Sauvegarde, restauration, mise à jour et retour arrière](docs/technique/sauvegarde-restauration.md).
- [Tests de bout en bout et gates de release](docs/technique/tests-e2e.md).
