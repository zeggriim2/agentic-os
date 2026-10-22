# Agentic OS — interface web

React 18 + TypeScript strict + Vite + Tailwind + TanStack Query + React Router. Build → `web/dist`, embarqué dans le binaire Go (`go:embed`).

```bash
npm ci
npm run dev       # http://127.0.0.1:5173, proxy /api et /webhooks → 127.0.0.1:8788
npm run lint      # tsc --noEmit
npm test          # vitest (jsdom)
npm run build     # tsc + vite build → dist/
```

`AOS_API_TARGET=http://127.0.0.1:XXXX npm run dev` change la cible du proxy (défaut `http://127.0.0.1:8788`).

Conventions :
- types de l'API : `src/api/types.ts` (contrat : `docs/technique/architecture.md` §9) ;
- client : `src/api/client.ts` (CSRF sur toute requête non-GET, 401 → /login, erreurs `{error:{code,message}}`) ;
- temps réel : `src/api/events.ts` (EventSource `/api/events`, backoff, invalidation TanStack par type) ;
- palette d'états unique : `src/lib/states.ts` + `<StateBadge>` ;
- dates : `src/lib/format.ts` (Europe/Paris via Intl) ;
- CSP : aucun script/style inline, aucune ressource externe, police système.
