// Copie le visualiseur de traces Playwright (application statique de playwright-core, sans réseau)
// dans dist/trace-viewer : la page « Enregistrement QA » l'ouvre sur /trace-viewer/?trace=<url>.
import { cpSync, existsSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const core = dirname(require.resolve("playwright-core/package.json"));
const src = join(core, "lib", "vite", "traceViewer");
const dst = join(import.meta.dirname, "..", "dist", "trace-viewer");
if (!existsSync(join(src, "index.html"))) {
  console.error(`visualiseur de traces introuvable : ${src}`);
  process.exit(1);
}
rmSync(dst, { recursive: true, force: true });
// Le mode « UI » (uiMode*) sert au lanceur de tests interactif : inutile ici.
cpSync(src, dst, { recursive: true, filter: (p) => !/uiMode|xtermModule/.test(p) });
console.log("visualiseur de traces copié dans dist/trace-viewer");
