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
| **Tickets en cours** | agents Développeur → Reviewer → QA, ticket par ticket | intervenir seulement sur demande (voir ci-dessous) |
| **Recette humaine** | vous (mode « Branche de feature ») | approuver la PR finale branche de feature → base, tester la branche, puis merger la PR finale sur GitHub |
| **Terminé** | — | tous les tickets sont terminés ou annulés (mode direct), ou PR finale mergée (mode branche de feature) |

### Intégration des tickets : directe ou branche de feature

Chaque feature a un **mode d'intégration**, choisi à sa création (pré-rempli avec le défaut du projet,
réglable dans « Modifier » du projet) et modifiable dans la carte **Intégration** de la feature :

- **Directe** : chaque ticket ouvre sa PR vers la branche de base du projet (ex. `develop`) et y est mergé.
- **Branche de feature** : au feu vert, Agentic OS fixe une branche `feature/F<n>-<titre-court>`, la crée
  depuis la base au premier développement ; chaque ticket part de cette branche, sa PR la vise et vous la
  mergez dedans. Quand tous les tickets y sont mergés, la feature passe en **Recette humaine** : Agentic OS
  demande l'autorisation d'ouvrir la **PR finale** (branche de feature → base, avec les « Closes #… » des
  issues : GitHub ne ferme une issue qu'au merge dans la branche par défaut). Vous testez la branche, puis
  mergez la PR finale : la feature passe en **Terminé**. Une PR finale refusée ou fermée sans merge bloque
  la feature ; **Relancer** redemande une PR finale.

Le mode reste modifiable **tant qu'aucun ticket n'est mergé**. Après le feu vert, passer en branche de
feature crée la branche et **redirige les PR déjà ouvertes** vers elle (et inversement en repassant en
direct). Si la base avance pendant la feature, Agentic OS ne rebase rien : la PR finale peut alors
présenter des conflits à résoudre sur GitHub.

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
- **Features** et **Pipeline** : où en est chaque feature et chaque ticket. Le pipeline a deux onglets :
  - **Features** : le cycle de vie d'une demande, de l'idée au feu vert, puis « Tickets en cours » avec
    l'avancement (« 1/3 mergé · T2 : revue de code ») et un lien « Voir les tickets » ;
  - **Tickets** : le travail des agents (développement, revue de code, QA, PR, merge humain), chaque
    carte rappelant sa feature.
  Un filtre par projet (mémorisé) s'applique aux deux ; « N ! » signale les éléments qui attendent une action humaine.
- **Runs** : chaque exécution d'agent, avec ses événements en direct, sa sortie et les tokens consommés.
  Les tokens sont séparés en **entrée nouvelle**, **relue du cache** et **sortie** : à chaque étape,
  l'agent renvoie tout son contexte au modèle, qui en relit l'essentiel depuis son cache, bien moins
  coûteux. Les « tokens nouveaux » (entrée nouvelle + sortie) reflètent la consommation réelle ; le total
  brut la surestime.
  **Arrêter le run** demande l'arrêt à Hermes ; le run reste « Arrêt en cours » tant que Hermes ne l'a
  pas confirmé.
- **Projets** : un projet = un dépôt GitHub, une branche de base et les checks CI obligatoires.
  Le mode **sandbox** sert aux essais ; le mode **product** protège un dépôt de production. Un projet
  utilisé par une feature active ne peut pas changer de dépôt ou de branche de base.
  **Graphe Git** (sur chaque ligne) : le workflow Git du dépôt en lecture seule — `main`, `develop`,
  branches de release, hotfix, features et tickets, avec les merges, les tags et les liens vers les PR,
  les commits et les tickets. Filtres : période (7 jours à 1 an) et types de branches (choix mémorisé).
  Les branches déjà intégrées (merge, squash) sont masquées par défaut ; **Branches déjà mergées** les
  réaffiche. Une branche mergée en squash puis modifiée en conflit par la suite n'est pas toujours
  reconnue : activer la suppression automatique des branches après merge sur GitHub évite ce cas.
- **Agents** : le profil Hermes de chaque rôle, le modèle souhaité (laisser vide pour ne pas en imposer)
  et le budget de tokens nouveaux par run (entrée nouvelle + sortie, cache non compté).
- **Projets → Modifier → Modèles par agent** : pour ce projet, le fournisseur et le modèle de chaque rôle,
  choisis dans la liste des fournisseurs authentifiés de Hermes (« Défaut du profil » = ne rien imposer ;
  le modèle souhaité de la page Agents s'applique alors). Si Hermes refuse le fournisseur demandé, le run
  est relancé une fois sur le modèle par défaut du profil et la page du run l'indique (« modèle demandé
  ≠ servi », « repli »).
- **Agents → Modèle par défaut des profils Hermes** : le modèle que chaque profil `aos-*` utilise par
  défaut, lu en direct dans Hermes, et une liste déroulante pour le changer (« Appliquer », avec
  confirmation). **Important** : Hermes vérifie toujours le modèle par défaut du profil avant d'appliquer
  celui demandé par le projet ; si ce défaut est indisponible (quota épuisé), le run échoue même avec un
  autre modèle demandé. Le bandeau rouge liste les rôles concernés (fournisseur demandé ≠ défaut du profil)
  avec un bouton **Aligner le profil**. La demande passe « En attente d'application », puis « Appliqué »
  en moins d'une minute : c'est le timer `aos-profile-sync` de l'hôte qui écrit la configuration du profil
  (une copie de l'ancienne est gardée). Si le bandeau jaune dit que le script ne tourne pas, l'exploitant
  lance `deploy/agents/install-profile-sync.sh` sur l'hôte Hermes. Seuls les profils `aos-*` sont
  modifiables ; le changement vaut pour les runs suivants.
- **Ticket → onglet « Enregistrement QA »** (projets avec « QA avec navigateur ») : la session navigateur
  du QA, enregistrée dans l'image des agents, sans réseau, sur l'application démarrée au commit testé :
  **vidéo**, **captures** rattachées aux critères d'acceptation (« Critère 2 » + son texte), étapes du
  scénario, problèmes relevés (erreurs JavaScript, requêtes en erreur, étape en échec) et bouton
  **Ouvrir la trace** : le visualiseur Playwright (servi par Agentic OS, rien n'est envoyé ailleurs)
  rejoue la session action par action, avec l'état de la page avant/après, le réseau et la console.
  Le QA enregistre une session quand le ticket modifie l'interface (`frontend/`, `templates/`,
  `assets/`) ou qu'un critère porte sur ce que voit l'utilisateur. Les enregistrements sont supprimés
  automatiquement après **15 jours**. Réglage : **Projets → Modifier → QA avec navigateur**.
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
