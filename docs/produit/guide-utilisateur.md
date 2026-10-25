# Guide d'utilisation d'Agentic OS (V1)

Agentic OS transforme une idée de fonctionnalité en Pull Requests **draft** sur GitHub, en faisant
travailler cinq agents Hermes (PO, Tech Lead, Développeur, Reviewer, QA). **Chaque étape qui engage
quelque chose passe par votre approbation, et le merge reste toujours le vôtre.**

## Le parcours d'une feature

| Étape (libellé à l'écran) | Qui agit | Ce que vous faites |
| --- | --- | --- |
| **Idée** | vous | créer la feature (titre + description de l'idée) puis **Démarrer la spec** |
| **Spécification** | agent PO | rien : suivre le run en direct si besoin |
| **Revue spec** | vous | relire user stories, critères d'acceptation et questions ouvertes → **Approuver** ou **Rejeter** avec un commentaire (le PO repart de votre commentaire) |
| **Design technique** | agent Tech Lead | rien |
| **Revue design** | vous | relire le découpage en tickets, risques et alternatives ; l'approbation affiche l'effet exact (« créer N issues sur owner/repo ») |
| **Issues ouvertes** | Agentic OS | les issues GitHub sont créées (sans doublon) |
| **Feu vert humain** | vous | donner le feu vert au développement |
| **En développement** | agents Développeur → Reviewer → QA, ticket par ticket | intervenir seulement sur demande (voir ci-dessous) |
| **Terminé** | — | tous les tickets sont terminés ou annulés |

### Le parcours d'un ticket

1. **Développement** : l'agent développeur commit dans un espace de travail isolé ; Agentic OS vérifie
   le travail (rien d'oublié, aucun test supprimé) et pousse la branche lui-même.
2. **Revue de code** puis **QA** : vérifient exactement ce commit. La QA doit couvrir **chaque**
   critère d'acceptation du ticket, ni plus ni moins.
3. **Changements demandés** : la revue ou la QA a trouvé un problème. Vous choisissez de relancer le
   développeur avec ces retours (**Approuver** le rework) ou d'abandonner le ticket (**Rejeter**).
4. **PR à approuver** : vous autorisez l'ouverture de la PR draft (branche, base et commit affichés ;
   si quelque chose a changé depuis, l'approbation est invalidée et redemandée).
5. **PR ouverte** puis **Merge humain** quand la CI est verte : relisez et mergez sur GitHub.
   Agentic OS détecte le merge et passe le ticket en **Terminé** (une PR fermée sans merge l'annule).

## Les écrans

- **Vue d'ensemble** : alertes, approbations en attente, éléments bloqués, runs actifs, tokens
  consommés, et le bouton **Mettre en pause** / **Reprendre** du dispatch.
- **Approbations** : tout ce qui attend votre décision. Le rejet demande un commentaire.
- **Features** et **Pipeline** : où en est chaque feature et chaque ticket.
- **Runs** : chaque exécution d'agent, avec ses événements en direct, sa sortie et les tokens consommés.
  **Arrêter le run** demande l'arrêt à Hermes ; le run reste « Arrêt en cours » tant que Hermes ne l'a
  pas confirmé.
- **Projets** : un projet = un dépôt GitHub, une branche de base et les checks CI obligatoires.
  Le mode **sandbox** sert aux essais ; le mode **product** protège un dépôt de production. Un projet
  utilisé par une feature active ne peut pas changer de dépôt ou de branche de base.
- **Agents** : le profil Hermes de chaque rôle, le modèle souhaité (laisser vide pour ne pas en imposer)
  et le budget de tokens par run.
- **Audit** : l'historique complet de qui a fait quoi.
- **Santé** : état de la base, de Hermes, de GitHub, de Telegram et des workers.

## Quand quelque chose bloque

- **Bloqué** : une étape a échoué après plusieurs tentatives (Hermes indisponible, budget de tokens
  dépassé, livrable invalide, branche modifiée par quelqu'un d'autre…). Ouvrez l'élément : l'erreur et
  la chronologie expliquent pourquoi. **Réessayer** reprend l'étape ; **Annuler** l'abandonne.
- **Mettre en pause** le dispatch avant une intervention (les travaux en cours se terminent, rien de
  nouveau ne démarre), puis **Reprendre**.
- Les notifications Telegram (si configurées) signalent les approbations à donner, les blocages, la CI
  verte (merge attendu) et les PR mergées ou fermées.

## Ce qu'Agentic OS ne fait jamais

- merger une PR ou pousser sur `main`/`develop` ;
- forcer un push (`force-push`) ;
- agir sur GitHub sans votre approbation pour les issues et les PR ;
- présumer qu'un agent est arrêté ou qu'un commit est bon sans l'avoir vérifié.

Pour l'installation et l'exploitation : [docs/technique/exploitation.md](../technique/exploitation.md).
