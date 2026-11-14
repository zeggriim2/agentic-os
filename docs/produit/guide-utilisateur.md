# Guide d'utilisation d'Agentic OS (V1)

Agentic OS transforme une idée de fonctionnalité en Pull Requests **draft** sur GitHub, en faisant
travailler cinq agents Hermes (PO, Tech Lead, Développeur, Reviewer, QA). **Chaque étape qui engage
quelque chose passe par votre approbation, et le merge reste toujours le vôtre.**

## Le parcours d'une feature

| Étape (libellé à l'écran) | Qui agit | Ce que vous faites |
| --- | --- | --- |
| **Idée** | vous | créer la feature (titre + description de l'idée) puis **Démarrer la spec** |
| **Spécification** | agent PO | rien : suivre le run en direct si besoin |
| **Revue spec** | vous | répondre aux questions du PO (un clic sur une réponse proposée, ou votre propre réponse ; « Laisser le PO décider » ; « Demander au PO »), commenter un critère → **Envoyer mes réponses au PO** (il révise la spec) ou **Approuver et lancer la conception** |
| **Design technique** | agent Tech Lead | rien |
| **Revue design** | vous | relire le découpage en tickets, décisions et risques, commenter un ticket ou une décision, répondre aux éventuelles questions du tech lead → **Renvoyer au tech lead avec mes remarques** ou **Approuver et créer N issues** (effet exact affiché) |
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

**Un défaut en recette ? « Demander une correction »** (page de la feature, en recette ou bloquée
depuis la recette) : décrivez le problème (page, étapes, attendu) et, si vous voulez, les critères que le
QA devra vérifier. Agentic OS crée un **ticket de correction** (marqué « Correction ») et son issue, sur
la même branche de feature ; la feature repasse en **Tickets en cours** et le ticket suit le cycle
habituel (développement depuis la branche à jour, revue, QA — enregistrée si le projet a le QA
navigateur —, PR vers la branche de feature, merge par vous). Quand il est mergé, la feature revient en
**Recette humaine** :
- PR finale **déjà ouverte** : elle reste la même (elle suit la branche) ; Agentic OS met à jour sa
  description (tickets intégrés, « Closes » de la nouvelle issue) ;
- PR finale **pas encore publiée** : son approbation en attente est retirée à la demande de correction,
  puis redemandée au retour en recette.
Pas de feu vert supplémentaire : celui de la feature couvre ses corrections (même dépôt, même branche,
PR toujours soumises à votre approbation). Vous pouvez demander plusieurs corrections successives.

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
   développeur (**Renvoyer au développeur**) en gardant seulement les constats avec lesquels vous êtes
   d'accord (« D'accord / Pas d'accord », « Demander au reviewer » en cas de doute), ou d'**abandonner le ticket**.
4. **PR à approuver** : vous autorisez l'ouverture de la PR draft (branche, base et commit affichés ;
   si quelque chose a changé depuis, l'approbation est invalidée et redemandée).
5. **PR ouverte** puis **Merge humain** quand la CI est verte : relisez et mergez sur GitHub.
   Agentic OS détecte le merge et passe le ticket en **Terminé** (une PR fermée sans merge l'annule).

## Les écrans

Thème **sombre par défaut** (bouton soleil/lune en bas du menu pour le thème clair). Code couleur :
**orange** = un agent travaille ; **bleu** = ça attend ta décision ; **vert** = terminé ; **rouge** = bloqué.

- **Tableau** (page d'accueil) : où en est chaque ticket, **en direct**, en six colonnes : Spec et
  conception, À démarrer, Développement, Revue, QA, **À toi**. Une carte change de colonne toute seule ;
  quand un agent y travaille, elle affiche son rôle, son dernier pas (« terminal · aos-ci all ») et ses
  tokens. Dans « À toi », la carte dit ce que tu dois faire (« Valider la spec », « Publier la PR »,
  « Merger la PR », « Faire la recette »). Un clic ouvre le ticket ou la feature. À droite :
  **En ce moment** (agents au travail) et **Activité** (le fil des derniers pas de tous les agents).
- **À décider** (bouton bleu en haut, avec le nombre en attente) : un tiroir avec toutes les décisions
  qui t'attendent, toutes features confondues. **Approuver** en un clic ; **Refuser** demande une raison
  (l'agent s'en sert) ; **Détails** ou **Ouvrir** pour aller voir avant de décider.
- **Ctrl+K** (ou la barre « Aller à, rechercher… ») : tape quelques lettres d'une feature, d'un ticket,
  d'une décision ou d'une page, ↑/↓ puis Entrée. « Nouvelle feature » ouvre directement le formulaire.
- **Fil d'équipe** (premier onglet de la page d'une feature) : les passages des agents (en orange, avec
  leur résumé et leurs tokens), tes décisions (en bleu) et vos échanges, dans l'ordre. En bas, écris aux
  agents (Ctrl+Entrée pour envoyer) :
  - **Consigne** : « À » = l'agent en cours par défaut. Elle lui est transmise tout de suite, sans le
    redémarrer. Si personne ne travaille (ou si tu vises un autre rôle), elle est gardée et donnée à
    l'agent à son prochain passage. Exemple : « Pense au cas où la configuration 2025 est incomplète. »
  - **Question** : choisis le rôle. Il répond dans sa session sur la feature, donc avec tout ce qu'il a
    déjà lu et fait, **sans rien modifier** (quelques milliers de tokens). Pas de question à un agent qui
    travaille : envoie-lui plutôt une consigne. Si une branche bouge pendant sa réponse, le fil le signale.
  - Pendant la recette, **« Transformer en correction »** sous une réponse ouvre « Demander une
    correction » déjà rempli avec la question et la réponse.

  Une consigne ou une réponse ne change jamais l'état de la feature : les décisions restent les tiennes.
- **Vue d'ensemble** : alertes, approbations en attente, éléments bloqués, runs actifs, tokens
  consommés, et le bouton **Mettre en pause** / **Reprendre** du dispatch.
- **Approbations** : tout ce qui attend votre décision. Chaque page de décision s'adapte : questions des
  agents en cartes (réponses proposées en un clic, champ libre toujours disponible, réponses gardées en
  brouillon si vous revenez plus tard), document lisible à côté, et deux boutons qui disent ce qu'ils
  font. Un renvoi à l'agent demande un retour (réponse, remarque ou commentaire).
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
