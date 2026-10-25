import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { request } from "@playwright/test";
import { AUTH_FILE, E2E_DIR, LOG_DIR, REPO_ROOT, STATE_FILE } from "./paths";
import type { E2EState } from "./env";

/**
 * Démarre l'environnement E2E complet et renvoie la fonction de nettoyage.
 *
 * Variables facultatives :
 * - AOS_E2E_BIN_DIR : dossier contenant des binaires déjà construits (`agentic-os`, `aos-e2e-fakes`) ;
 * - AOS_E2E_SKIP_WEB_BUILD=1 : réutiliser web/dist existant (sinon `npm ci && npm run build`) ;
 * - AOS_E2E_PG_PORT : port hôte du PostgreSQL jetable (sinon un port libre) ;
 * - AOS_E2E_PG_IMAGE : image PostgreSQL (défaut postgres:16-alpine) ;
 * - AOS_E2E_GIT : binaire git absolu (défaut /usr/bin/git) ;
 * - AOS_E2E_KEEP=1 : conserver le dossier temporaire (workspaces, dépôts bare) après les tests.
 */
export default async function globalSetup(): Promise<() => Promise<void>> {
  const cleanups: (() => void | Promise<void>)[] = [];
  const teardown = async () => {
    for (const fn of cleanups.reverse()) {
      try {
        await fn();
      } catch (e) {
        console.error("[e2e] nettoyage :", e);
      }
    }
  };
  try {
    await setup(cleanups);
  } catch (e) {
    await teardown();
    throw e;
  }
  return teardown;
}

function log(msg: string) {
  console.log(`[e2e] ${msg}`);
}

function run(cmd: string, args: string[], opts: { cwd?: string; env?: NodeJS.ProcessEnv; input?: string } = {}): string {
  return execFileSync(cmd, args, {
    cwd: opts.cwd ?? REPO_ROOT,
    env: opts.env ?? process.env,
    input: opts.input,
    stdio: [opts.input !== undefined ? "pipe" : "ignore", "pipe", "inherit"],
    encoding: "utf8",
    maxBuffer: 64 << 20,
  });
}

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const port = (srv.address() as net.AddressInfo).port;
      srv.close(() => resolve(port));
    });
  });
}

const secret = (n = 32) => randomBytes(n).toString("base64url");

function writeSecret(dir: string, name: string, value: string) {
  const p = path.join(dir, name);
  fs.mkdirSync(path.dirname(p), { recursive: true, mode: 0o700 });
  fs.writeFileSync(p, value + "\n", { mode: 0o600 });
}

async function waitFor(what: string, fn: () => Promise<boolean>, timeoutMs: number, proc?: ChildProcess) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (proc && proc.exitCode !== null) throw new Error(`${what} : le processus s'est arrêté (code ${proc.exitCode}), voir ${LOG_DIR}`);
    try {
      if (await fn()) return;
    } catch {
      /* pas encore prêt */
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`${what} : délai dépassé (${timeoutMs} ms)`);
}

async function httpOK(url: string): Promise<boolean> {
  const res = await fetch(url);
  return res.ok;
}

function startProcess(name: string, bin: string, args: string[], env: NodeJS.ProcessEnv, cleanups: (() => void | Promise<void>)[]): ChildProcess {
  const out = fs.openSync(path.join(LOG_DIR, `${name}.log`), "w");
  const child = spawn(bin, args, { env, stdio: ["ignore", out, out] });
  fs.closeSync(out);
  cleanups.push(async () => {
    if (child.exitCode !== null) return;
    child.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        child.kill("SIGKILL");
        resolve();
      }, 10_000);
      child.once("exit", () => {
        clearTimeout(t);
        resolve();
      });
    });
  });
  return child;
}

async function setup(cleanups: (() => void | Promise<void>)[]) {
  fs.rmSync(LOG_DIR, { recursive: true, force: true });
  fs.mkdirSync(LOG_DIR, { recursive: true });
  const work = fs.mkdtempSync(path.join(os.tmpdir(), "aos-e2e-"));
  cleanups.push(() => {
    if (process.env.AOS_E2E_KEEP === "1") log(`dossier conservé : ${work}`);
    else fs.rmSync(work, { recursive: true, force: true });
  });
  log(`dossier de travail : ${work}`);

  // 1. Binaires (UI embarquée puis Go).
  let binDir = process.env.AOS_E2E_BIN_DIR;
  if (!binDir) {
    binDir = path.join(work, "bin");
    if (process.env.AOS_E2E_SKIP_WEB_BUILD !== "1") {
      log("construction de l'UI (web : npm ci && npm run build)…");
      run("npm", ["ci", "--no-audit", "--no-fund"], { cwd: path.join(REPO_ROOT, "web") });
      run("npm", ["run", "build"], { cwd: path.join(REPO_ROOT, "web") });
      // vite vide web/dist : on recrée le marqueur versionné (go:embed exige un dossier non vide).
      fs.writeFileSync(path.join(REPO_ROOT, "web", "dist", ".gitkeep"), "");
    }
    if (!fs.existsSync(path.join(REPO_ROOT, "web", "dist", "index.html"))) {
      throw new Error("web/dist/index.html absent : construisez l'UI (cd web && npm ci && npm run build)");
    }
    log("compilation de agentic-os et des faux services…");
    run("go", ["build", "-o", path.join(binDir, "agentic-os"), "./cmd/agentic-os"]);
    run("go", ["build", "-o", path.join(binDir, "aos-e2e-fakes"), "./e2e/fakes"]);
  }
  const aos = path.join(binDir, "agentic-os");
  const fakesBin = path.join(binDir, "aos-e2e-fakes");
  for (const b of [aos, fakesBin]) if (!fs.existsSync(b)) throw new Error(`binaire absent : ${b}`);

  // 2. Secrets (fichiers 0600 dans un dossier 0700).
  const secrets = path.join(work, "secrets");
  fs.mkdirSync(secrets, { mode: 0o700 });
  // Identifiant de connexion de test tiré au hasard à chaque exécution (jamais en dur).
  const adminCredential = secret(24);
  writeSecret(secrets, "session_key", secret(48));
  writeSecret(secrets, "github_token", `ghs_e2e_${secret(24)}`);
  writeSecret(secrets, "github_webhook_secret", secret(32));
  writeSecret(secrets, "telegram_bot_token", `123456:${secret(24)}`);
  writeSecret(secrets, "telegram_chat_id", "424242");
  for (const profile of ["aos-po", "aos-tech-lead", "aos-developer", "aos-reviewer", "aos-qa"]) {
    writeSecret(secrets, `hermes/${profile}.key`, secret(32));
  }
  run(aos, ["admin-password", "-o", path.join(secrets, "admin_password_hash")], { input: adminCredential + "\n" });

  // 3. PostgreSQL jetable.
  const pgPort = Number(process.env.AOS_E2E_PG_PORT || (await freePort()));
  const pgPassword = secret(16);
  const pgName = `aos-e2e-pg-${randomBytes(4).toString("hex")}`;
  log(`PostgreSQL ${pgName} sur 127.0.0.1:${pgPort}…`);
  run("docker", [
    "run", "-d", "--rm", "--name", pgName,
    "-e", `POSTGRES_PASSWORD=${pgPassword}`,
    "-p", `127.0.0.1:${pgPort}:5432`,
    process.env.AOS_E2E_PG_IMAGE || "postgres:16-alpine",
  ]);
  cleanups.push(() => {
    try {
      run("docker", ["rm", "-f", pgName]);
    } catch {
      /* déjà supprimé */
    }
  });
  await waitFor(
    "PostgreSQL",
    async () => {
      run("docker", ["exec", pgName, "pg_isready", "-U", "postgres", "-h", "127.0.0.1"]);
      return true;
    },
    90_000,
  );
  const dsnFile = path.join(secrets, "database_url");
  writeSecret(secrets, "database_url", `postgres://postgres:${pgPassword}@127.0.0.1:${pgPort}/postgres?sslmode=disable`);

  // 4. Ports et environnement d'Agentic OS.
  const appPort = await freePort();
  const fakesPort = await freePort();
  const baseURL = `http://127.0.0.1:${appPort}`;
  const fakesURL = `http://127.0.0.1:${fakesPort}`;
  const workspaces = path.join(work, "workspaces");
  const remotes = path.join(work, "remotes");
  fs.mkdirSync(workspaces, { recursive: true });
  fs.mkdirSync(remotes, { recursive: true });
  const git = process.env.AOS_E2E_GIT || "/usr/bin/git";
  const env: NodeJS.ProcessEnv = {
    PATH: process.env.PATH,
    HOME: work,
    TMPDIR: work,
    LANG: "C.UTF-8",
    AOS_HTTP_ADDR: `127.0.0.1:${appPort}`,
    AOS_DATABASE_URL_FILE: dsnFile,
    AOS_SECRETS_DIR: secrets,
    AOS_WORKSPACE_ROOT: workspaces,
    AOS_HERMES_URL: fakesURL,
    AOS_GITHUB_API_URL: fakesURL,
    AOS_GITHUB_WEB_URL: `file://${remotes}`,
    AOS_TELEGRAM_API_URL: fakesURL,
    AOS_GIT_BINARY: git,
    AOS_WORKERS: "2",
    AOS_LEASE_SECONDS: "60",
  };

  await waitFor(
    "migrations",
    async () => {
      run(aos, ["migrate"], { env });
      return true;
    },
    60_000,
  );

  // 5. Faux services puis Agentic OS.
  const fakes = startProcess(
    "fakes",
    fakesBin,
    [
      "-addr", `127.0.0.1:${fakesPort}`,
      "-secrets-dir", secrets,
      "-repos-root", remotes,
      "-workspace-root", workspaces,
      "-git", git,
      "-web-url", "https://github.e2e.test",
      "-webhook-url", `${baseURL}/webhooks/github`,
      "-checks", "tests",
      "-seed", "o/demo,o/rejet,o/divers",
    ],
    { PATH: process.env.PATH, HOME: work, TMPDIR: work },
    cleanups,
  );
  await waitFor("faux services", () => httpOK(`${fakesURL}/__e2e/health`), 30_000, fakes);
  const app = startProcess("agentic-os", aos, ["serve"], env, cleanups);
  await waitFor("agentic-os /healthz", () => httpOK(`${baseURL}/healthz`), 60_000, app);
  await waitFor("agentic-os /readyz", () => httpOK(`${baseURL}/readyz`), 30_000, app);
  log(`Agentic OS prêt sur ${baseURL} (faux services ${fakesURL})`);

  // 6. État partagé + session administrateur (une seule connexion : le login est limité à 5/min).
  const state: E2EState = { baseURL, fakesURL, login: "admin", password: adminCredential, workDir: work };
  fs.mkdirSync(path.dirname(STATE_FILE), { recursive: true, mode: 0o700 });
  fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2), { mode: 0o600 });
  cleanups.push(() => fs.rmSync(path.dirname(STATE_FILE), { recursive: true, force: true }));
  fs.mkdirSync(path.dirname(AUTH_FILE), { recursive: true, mode: 0o700 });
  const ctx = await request.newContext({ baseURL });
  const res = await ctx.post("/api/auth/login", { data: { login: state.login, password: adminCredential } });
  if (res.status() !== 200) throw new Error(`connexion initiale refusée : HTTP ${res.status()}`);
  await ctx.storageState({ path: AUTH_FILE });
  fs.chmodSync(AUTH_FILE, 0o600);
  await ctx.dispose();
  cleanups.push(() => fs.rmSync(path.dirname(AUTH_FILE), { recursive: true, force: true }));
  log(`environnement prêt (journaux : ${path.relative(E2E_DIR, LOG_DIR)}/)`);
}
