// Exécuteur des scénarios navigateur du QA (Agentic OS) : enregistre la session (vidéo + trace
// Playwright), fournit des aides (connexion, captures liées aux critères) et écrit report.json.
//   node aos-qa-runner.mjs <scénario.mjs> <dossier de sortie> <URL de base>
// Le scénario exporte par défaut : async ({ page, context, baseURL, login, shot, step }) => { … }
// Sans réseau : toute requête hors de l'application locale est bloquée (polices externes, CDN…).
import { mkdirSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { chromium } from "playwright";

const [scenarioPath, outDir, baseURL] = process.argv.slice(2);
if (!scenarioPath || !outDir || !baseURL) {
  console.error("usage : aos-qa-runner.mjs <scénario.mjs> <dossier de sortie> <URL de base>");
  process.exit(2);
}
const TIMEOUT_MS = Number(process.env.AOS_QA_TIMEOUT_MS ?? 180_000);
const MAX_SHOTS = 40;
const LOGIN = { email: process.env.AOS_QA_EMAIL ?? "alice@example.com", password: process.env.AOS_QA_PASSWORD ?? "Test1234!" };
const origin = new URL(baseURL).origin;

const report = { ok: false, error: null, duration_ms: 0, steps: [], shots: [], console_errors: [], failed_requests: [], blocked_external: 0 };
const push = (list, v) => list.length < 50 && list.push(String(v).slice(0, 500));
const slug = (s) => s.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").slice(0, 40).replace(/^-+|-+$/g, "") || "capture";

const videoTmp = join(outDir, ".video");
mkdirSync(join(outDir, "shots"), { recursive: true });
const t0 = Date.now();
const browser = await chromium.launch({ args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const context = await browser.newContext({
  baseURL,
  viewport: { width: 1280, height: 800 },
  locale: "fr-FR",
  timezoneId: "Europe/Paris",
  recordVideo: { dir: videoTmp, size: { width: 1280, height: 800 } },
});
context.setDefaultTimeout(15_000);
await context.route("**/*", (route) => {
  if (new URL(route.request().url()).origin === origin) return route.continue();
  report.blocked_external++;
  return route.abort("blockedbyclient");
});
await context.tracing.start({ screenshots: true, snapshots: true, sources: false });
const page = await context.newPage();
page.on("pageerror", (e) => push(report.console_errors, e.message ?? e));
page.on("console", (m) => m.type() === "error" && !/ERR_BLOCKED_BY_CLIENT/.test(m.text()) && push(report.console_errors, m.text()));
page.on("response", (r) => r.status() >= 400 && new URL(r.url()).origin === origin && push(report.failed_requests, `${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`));

const helpers = {
  page,
  context,
  baseURL,
  /** Connexion avec le compte de démonstration (fixtures) ou un autre compte. */
  async login(email = LOGIN.email, password = LOGIN.password) {
    await page.goto("/login");
    await page.fill('input[type="email"]', email);
    await page.fill('input[type="password"]', password);
    await page.getByRole("button", { name: /se connecter/i }).click();
    await page.waitForURL((u) => !u.pathname.startsWith("/login"));
  },
  /** Capture pleine page, rattachée au critère d'acceptation n° criterion (1 = premier critère). */
  async shot(label, opts = {}) {
    if (report.shots.length >= MAX_SHOTS) throw new Error(`plus de ${MAX_SHOTS} captures`);
    const file = `shots/${String(report.shots.length + 1).padStart(2, "0")}-${slug(String(label))}.png`;
    await page.screenshot({ path: join(outDir, file), fullPage: opts.fullPage ?? true });
    const criterion = Number.isInteger(opts.criterion) && opts.criterion > 0 ? opts.criterion : null;
    report.shots.push({ file, label: String(label).slice(0, 200), criterion });
    return file;
  },
  /** Étape nommée : son résultat (succès, durée, erreur) apparaît dans le rapport. */
  async step(name, fn) {
    const s = Date.now();
    try {
      const v = await fn();
      report.steps.push({ name: String(name).slice(0, 200), ok: true, ms: Date.now() - s });
      return v;
    } catch (e) {
      report.steps.push({ name: String(name).slice(0, 200), ok: false, ms: Date.now() - s, error: String(e.message ?? e).split("\n")[0].slice(0, 500) });
      throw e;
    }
  },
};

let timer;
try {
  const mod = await import(pathToFileURL(resolve(scenarioPath)).href);
  if (typeof mod.default !== "function") throw new Error("le scénario doit exporter une fonction par défaut");
  await Promise.race([
    mod.default(helpers),
    new Promise((_, rej) => (timer = setTimeout(() => rej(new Error(`scénario interrompu après ${TIMEOUT_MS / 1000} s`)), TIMEOUT_MS))),
  ]);
  report.ok = true;
} catch (e) {
  report.error = String(e.message ?? e).split("\n")[0].slice(0, 500);
  await helpers.shot("état à l'échec").catch(() => {});
} finally {
  clearTimeout(timer);
  await context.tracing.stop({ path: join(outDir, "trace.zip") }).catch(() => {});
  await context.close().catch(() => {});
  await browser.close().catch(() => {});
  for (const f of readdirSync(videoTmp, { withFileTypes: true })) if (f.isFile() && f.name.endsWith(".webm")) renameSync(join(videoTmp, f.name), join(outDir, "video.webm"));
  rmSync(videoTmp, { recursive: true, force: true });
  report.duration_ms = Date.now() - t0;
  writeFileSync(join(outDir, "report.json"), JSON.stringify(report, null, 2));
}
const n = (l, w) => `${l.length} ${w}`;
console.log(
  `aos-qa-browser : scénario ${report.ok ? "OK" : "ÉCHEC"} (${Math.round(report.duration_ms / 1000)} s) — ${n(report.shots, "capture(s)")}, ` +
    `${n(report.console_errors, "erreur(s) JS")}, ${n(report.failed_requests, "requête(s) ≥ 400")}, ${report.blocked_external} requête(s) externe(s) bloquée(s)`,
);
if (report.error) console.log(`erreur : ${report.error}`);
for (const s of report.steps.filter((s) => !s.ok)) console.log(`étape en échec : ${s.name} — ${s.error}`);
for (const e of report.console_errors.slice(0, 5)) console.log(`console : ${e}`);
for (const r of report.failed_requests.slice(0, 5)) console.log(`requête : ${r}`);
process.exit(report.ok ? 0 : 1);
