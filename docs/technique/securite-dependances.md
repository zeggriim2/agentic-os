# Sécurité des dépendances

## Politique

- **Toolchain Go supportée uniquement** : Go publie des correctifs pour les deux dernières versions
  majeures. `go.mod` déclare `go 1.27` ; la CI (`go-version-file: go.mod`) et l'image
  (`golang:1.27-alpine3.24`) suivent la même version. Passer à la version suivante dès sa sortie
  (la 1.27 sort du support à la publication de la 1.29).
- **Aucune vulnérabilité connue acceptée sans décision écrite** :
  - `govulncheck ./...` doit sortir sans vulnérabilité **appelée** par notre code (les
    vulnérabilités d'un module requis mais non appelées sont signalées sans bloquer) ;
  - `npm audit --audit-level=moderate` doit être vide sur **toutes** les dépendances web, dev
    comprises (Vitest et Vite exécutent du code sur la machine de développement et en CI).
- **Reproductibilité** : `go.sum` et `web/package-lock.json` sont commités ; `npm ci` en CI et dans
  l'image, avec `--ignore-scripts` (aucun script d'installation tiers n'est exécuté) ;
  `npm audit signatures` vérifie les attestations du registre.
- **Épinglage par empreinte** : images Docker (`deploy/Dockerfile`, `deploy/compose.yaml`, service
  PostgreSQL de la CI) et actions GitHub épinglées par digest/SHA de commit, version lisible en
  commentaire. Les tags mutables ne sont pas utilisés.
- **Mises à jour** : Dependabot (`.github/dependabot.yml`) ouvre chaque lundi des PR vers `develop`
  pour Go, npm, actions GitHub et images Docker. Elles passent la CI et le merge reste humain.
- **Surveillance continue** : le workflow `security` tourne sur chaque push/PR et chaque lundi, pour
  détecter les nouvelles CVE sur du code inchangé.

## Contrôles en CI

| Workflow | Job | Échoue si |
| --- | --- | --- |
| `security` | `govulncheck` | une vulnérabilité Go (stdlib ou module) est atteignable depuis notre code |
| `security` | `npm audit` | une vulnérabilité npm ≥ moderate, ou une signature de registre invalide |
| `tests` | `tests` | tests, `go vet`, `gofmt`, build ou tests web en échec |

Les jobs `govulncheck` et `npm audit` sont à ajouter aux checks requis de la protection de branche
de `develop` et `main` (réglage GitHub, droits admin).

## Remédiation

1. `govulncheck ./...` / `npm audit` en local pour identifier le module et la version corrigée.
2. Mise à jour minimale qui corrige (`go get module@version && go mod tidy`, ou `npm install
   paquet@version`). Pour une dépendance transitive bloquée par un parent non maintenu : entrée
   `overrides` dans `web/package.json` + vérification du build et des tests.
3. Si aucun correctif n'existe : documenter ici la vulnérabilité, l'analyse d'exposition et la date
   de réévaluation, avant tout merge.

## Exceptions en vigueur

- `overrides` npm : `postcss-selector-parser` ^7.1.7 et `postcss-nested` ^7.0.2 imposés à
  Tailwind CSS 3.4 (GHSA-rj75-hqrm-r3gf, complexité quadratique). Le CSS généré a été comparé avant et
  après : mêmes classes utilitaires, seules les normalisations du minifieur de Vite 8 diffèrent. À retirer
  lors du passage à Tailwind 4.

## Hors périmètre (à traiter côté hôte)

- Scan des paquets système de l'image : l'image d'exécution part d'`alpine:3.24` épinglée, avec
  `apk upgrade` au build. Aucun scanner d'image tiers n'est branché en CI (risque de chaîne
  d'approvisionnement des actions de scan) ; un scan ponctuel avant release est prévu dans la
  checklist de release.
- Isolation des credentials et du réseau des profils Hermes développeurs (configuration Hermes de
  l'hôte, voir la documentation d'exploitation).
