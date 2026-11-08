# Maquettes du cockpit Agentic OS (étape 1)

Trois maquettes HTML jetables, autonomes, sans dépendance externe. Ouvre `index.html` dans un navigateur
d'ordinateur. Le « direct » est simulé : laisse la page tourner environ 20 s, puis essaie la discussion.

| | A · Salle de contrôle | B · Parcours de la feature | C · Fil d'équipe |
|---|---|---|---|
| Idée | Tableau des tickets en direct + panneau de discussion permanent | Le chemin de la feature, de l'idée au merge, avec le travail en cours en détail | Une conversation par feature : agents, décisions et toi dans le même fil |
| Point fort | Vue d'ensemble, plusieurs tickets à la fois | Comprendre où on en est et pourquoi | Le plus naturel pour échanger avec les agents |
| Point faible | Plus chargé | Une seule feature à la fois | Moins de vue d'ensemble sur les tickets |
| Décisions | Tiroir « À décider » + compteur | Dans le parcours, à l'étape concernée | Cartes dans le fil, comme un message |
| Thème | Clair, minéral, cobalt pour les agents | Sombre ardoise, sarcelle pour les agents, sable pour tes décisions | Clair, violet pour les agents, ocre pour tes décisions |

Commun aux trois : consigne pendant le travail ou question, réponse transformable en correction,
approbation en un clic, budget en tokens en direct, Ctrl+K (actif dans A).

## D · Cockpit (A + C), retenue

`d-cockpit/index.html` : l'accueil est le tableau en direct de A (tickets de toutes les features, équipe
et activité à droite) ; un clic sur un ticket ouvre le fil d'équipe de C pour sa feature (messages,
travail repliable, décisions en carte, consigne ou question). « À décider », Ctrl+K et le direct sont
communs aux deux écrans. Couleurs : cobalt pour les agents au travail, ocre pour tes décisions.

Ces fichiers ne sont pas du code de production : la maquette retenue sera réimplémentée dans `web/`.
