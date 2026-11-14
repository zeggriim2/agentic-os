# Maquette — pages de décision (1.13)

Fichier : [`index.html`](index.html), autonome : à ouvrir dans un navigateur, sans serveur. Écran
1440 × 900, thème sombre par défaut (bouton Clair/Sombre mémorisé). Mêmes couleurs que le cockpit :
**orange** = agents et actions, **bleu** = tes décisions, **vert** = traité.

La page d'approbation existante (`/approvals/{id}`) s'adapte au type de décision. Trois écrans :

1. **Revue de spec** — avec la vraie spec de F7 et ses 13 questions ouvertes.
   - Chaque question est une carte : réponses proposées en un clic, champ libre, « Laisser le PO
     décider », « Demander au PO » (réponse dans le fil d'équipe, avec le contexte du PO).
   - La spec est lisible à droite ; chaque critère d'acceptation peut être commenté.
   - Compteur « n / 13 traitées », enregistrement au fil de l'eau, filtre « À traiter ».
   - Deux décisions explicites : **« Envoyer mes réponses au PO »** (remplace « Rejeter » : le PO
     révise avec une liste question → réponse) et **« Approuver et lancer la conception »**, qui
     prévient s'il reste des questions sans réponse.
2. **Revue de conception** (exemple) — tickets proposés commentables (« Fusionner avec T2 »),
   décisions et alternatives écartées, risques, questions du tech lead avec la même mécanique.
3. **Reprise après revue** (exemple F6) — constats du reviewer avec « D'accord / Pas d'accord »
   (le développeur ne traite que ceux que tu gardes), « Demander au reviewer », remarque libre,
   boutons « Renvoyer au développeur (tentative 2 / 3) » et « Abandonner le ticket ».

Les autres décisions (feu vert, publication de PR, PR finale) gardent leur contenu et reçoivent
des boutons explicites et « Demander à l'agent ».
