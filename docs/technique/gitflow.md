# Agentic OS — workflow GitFlow

Ce document définit le workflow Git du dépôt Agentic OS. Il combine GitFlow avec des branches de support afin de maintenir plusieurs lignes de versions en parallèle.

## Principes

- `main` contient uniquement des versions publiées et déployables.
- `develop` est la branche d’intégration de la prochaine version.
- aucun push direct n’est autorisé sur `main`, `develop`, `release/*` ou `support/*` ; tout changement passe par une Pull Request et une CI verte ;
- le merge et la publication restent des décisions humaines ;
- une version publiée reçoit un tag annoté conforme à SemVer : `vMAJOR.MINOR.PATCH` ;
- les branches temporaires sont supprimées après leur intégration ; les branches `support/*` restent tant que la ligne est maintenue.

## Branches permanentes

### `main`

Branche de production. Chaque commit de publication est associé à un tag `vX.Y.Z`. Les fonctionnalités ne partent jamais directement de `main`.

### `develop`

Branche d’intégration de la prochaine version. Les branches de fonctionnalité et les corrections ordinaires partent de `develop` et y reviennent par Pull Request.

### `support/X.Y`

Branche optionnelle conservant une ancienne ligne stable après la publication d’une version majeure plus récente. Elle n’accepte que des corrections compatibles avec cette ligne.

Exemple : après la publication de `v2.0.0`, `support/1.1` permet de publier `v1.1.1` sans réintroduire les changements de la version 2.

## Branches temporaires

### `feature/<ticket>-<description>`

- créée depuis `develop` ;
- contient une fonctionnalité ou un ticket isolé ;
- fusionnée dans `develop` par squash merge ;
- supprimée après fusion.

Les branches automatisées `aos/<ticket-key>` créées par Agentic OS sont considérées comme des branches de fonctionnalité. Pour appliquer ce workflow à un projet piloté par Agentic OS, son `base_branch` doit être `develop`.

Exemples :

```text
feature/AOS-35-project-creation
feature/AOS-42-github-webhooks
aos/agentic-os-F1-T1
```

### `fix/<ticket>-<description>`

Correction non urgente destinée à la prochaine version. Même cycle que `feature/*` : départ de `develop`, Pull Request vers `develop`, squash merge.

### `release/X.Y.Z`

- créée depuis `develop` lorsque le périmètre de la version est gelé ;
- n’accepte plus que stabilisation, documentation, métadonnées de version et corrections bloquantes ;
- fusionnée par merge commit dans `main` ;
- le commit de publication sur `main` reçoit le tag annoté `vX.Y.Z` ;
- fusionnée ensuite dans `develop` pour récupérer toutes les corrections de stabilisation ;
- supprimée après publication et synchronisation.

### `hotfix/X.Y.Z`

Correction urgente de la version de production courante :

- créée depuis le tag ou le commit de production concerné sur `main` ;
- fusionnée dans `main` par merge commit ;
- taguée `vX.Y.Z` sur `main` ;
- reportée dans `develop` et dans toute branche `support/*` concernée ;
- supprimée après propagation et vérification.

### `hotfix/X.Y.Z` pour une ancienne version maintenue

Pour corriger une ligne ancienne :

- créer la branche depuis `support/X.Y`, pas depuis le `main` d’une version majeure plus récente ;
- fusionner la correction dans `support/X.Y` ;
- poser le tag `vX.Y.Z` sur le commit de publication de cette branche ;
- reporter la correction vers les versions plus récentes avec une Pull Request ou un cherry-pick documenté si elle reste applicable.

## Stratégie de merge

| Source | Cible | Méthode |
|---|---|---|
| `feature/*`, `fix/*`, `aos/*` | `develop` | squash merge |
| `release/*` | `main` | merge commit (`--no-ff`) |
| `release/*` | `develop` | merge commit (`--no-ff`) |
| `hotfix/*` | `main` ou `support/*` | merge commit (`--no-ff`) |
| propagation d’un hotfix | `develop` et lignes supportées | PR avec merge commit ou cherry-pick documenté |

Les branches `release/*` et `hotfix/*` conservent un merge commit afin que la topologie des publications reste lisible. Les fonctionnalités sont squashées afin de garder un historique d’intégration compact.

## Versionnement et maintenance

- `MAJOR` : rupture de compatibilité ;
- `MINOR` : fonctionnalité rétrocompatible ;
- `PATCH` : correction rétrocompatible ;
- format du tag : `vX.Y.Z` ;
- une branche `support/X.Y` ne reçoit que des versions `vX.Y.PATCH` ;
- les versions supportées et leur date de fin de support doivent être listées dans les notes de version.

## Quality gates

Une Pull Request ne peut être fusionnée que si :

1. la branche cible est à jour ou la PR est fusionnable sans conflit ;
2. le formatage et le lint passent ;
3. les tests Go et frontend passent ;
4. les tests d’intégration PostgreSQL passent lorsqu’ils sont concernés ;
5. le build Docker passe ;
6. la revue humaine est approuvée ;
7. pour `main`, la version et les notes de publication sont validées.

## Cycle normal

```text
develop
  └── feature/AOS-35-example
        └── PR + squash → develop

develop
  └── release/1.0.0
        ├── PR + merge commit → main → tag v1.0.0
        └── PR + merge commit → develop
```

## Correction urgente

```text
main (v1.0.0)
  └── hotfix/1.0.1
        ├── PR + merge commit → main → tag v1.0.1
        └── propagation → develop
```

## Maintenance parallèle

```text
main (v2.0.0)
support/1.1
  └── hotfix/1.1.1
        ├── PR + merge commit → support/1.1 → tag v1.1.1
        └── propagation vers develop/main si applicable
```

## Initialisation du dépôt Agentic OS

La mise en place initiale suit ce chemin :

1. créer `develop` depuis le `main` initial ;
2. conserver l’implémentation V1 sur `feature/bootstrap-v1` ;
3. ouvrir une Pull Request `feature/bootstrap-v1` → `develop` ;
4. après validation, créer `release/1.0.0` depuis `develop` ;
5. stabiliser puis fusionner la release dans `main` et poser le tag `v1.0.0` ;
6. réintégrer la release dans `develop`.
