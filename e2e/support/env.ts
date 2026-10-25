import fs from "node:fs";
import { expect, test as base, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import { AUTH_FILE, STATE_FILE } from "./paths";

export interface E2EState {
  baseURL: string;
  fakesURL: string;
  login: string;
  password: string;
  workDir: string;
}

export function readState(): E2EState {
  return JSON.parse(fs.readFileSync(STATE_FILE, "utf8")) as E2EState;
}

/** Client de l'API Agentic OS avec la session du contexte (cookie) et l'en-tête CSRF. */
export class Api {
  private csrf: string | null = null;
  constructor(private readonly req: APIRequestContext) {}

  private async token(): Promise<string> {
    if (!this.csrf) {
      const me = await this.req.get("/api/auth/me");
      expect(me.status(), "session API").toBe(200);
      this.csrf = ((await me.json()) as { csrf_token: string }).csrf_token;
    }
    return this.csrf;
  }

  async get<T = any>(path: string): Promise<T> {
    const res = await this.req.get(path);
    expect(res.status(), `GET ${path}`).toBe(200);
    return (await res.json()) as T;
  }

  async send<T = any>(method: "POST" | "PATCH", path: string, data: unknown, expected = [200, 201, 202]): Promise<T> {
    const res = await this.req.fetch(path, { method, data, headers: { "X-CSRF-Token": await this.token() } });
    expect(expected, `${method} ${path} → ${res.status()} ${await res.text()}`).toContain(res.status());
    return (await res.json()) as T;
  }

  /** Crée (si besoin) un projet sandbox activé ; renvoie son id. */
  async ensureProject(slug: string, repository: string, requiredChecks = ["tests"]): Promise<number> {
    const list = await this.get<{ items: { id: number; slug: string; enabled: boolean }[] }>("/api/projects?limit=200");
    let p = list.items.find((x) => x.slug === slug);
    if (!p) {
      p = await this.send("POST", "/api/projects", {
        slug, name: `Projet ${slug}`, repository, base_branch: "main", required_checks: requiredChecks, mode: "sandbox", test_command: "true",
      });
    }
    if (!p!.enabled) await this.send("PATCH", `/api/projects/${p!.id}`, { enabled: true });
    return p!.id;
  }

  async createFeature(projectId: number, title: string): Promise<number> {
    const f = await this.send<{ id: number }>("POST", "/api/features", {
      project_id: projectId, title, description: `Description de « ${title} » pour le PO (test E2E).`,
    });
    return f.id;
  }

  async featureAction(id: number, action: "start_spec" | "cancel" | "retry") {
    await this.send("POST", `/api/features/${id}/actions`, { action });
  }

  async feature(id: number): Promise<any> {
    return this.get(`/api/features/${id}`);
  }

  /** Attend un état de feature (le moteur sonde Hermes toutes les 3 s). */
  async waitFeatureState(id: number, state: string, timeout = 90_000): Promise<any> {
    let last: any;
    await expect
      .poll(async () => (last = await this.feature(id)).feature.state, { timeout, intervals: [500, 1000, 2000], message: `feature ${id} → ${state}` })
      .toBe(state);
    return last;
  }

  /** Attend puis renvoie l'approbation en attente du type donné pour une feature. */
  async pendingApproval(featureId: number, kind: string, timeout = 90_000): Promise<{ id: number; title: string }> {
    let found: any;
    await expect
      .poll(
        async () => {
          const f = await this.feature(featureId);
          found = f.approvals.find((a: any) => a.kind === kind && a.status === "pending");
          return Boolean(found);
        },
        { timeout, intervals: [500, 1000, 2000], message: `approbation ${kind} de la feature ${featureId}` },
      )
      .toBe(true);
    return found;
  }

  async decide(approvalId: number, decision: "approved" | "rejected", comment = "") {
    await this.send("POST", `/api/approvals/${approvalId}/decision`, { decision, comment });
  }
}

/** Client des points de contrôle des faux services. */
export class Fakes {
  constructor(private readonly req: APIRequestContext, private readonly url: string) {}

  async post<T = any>(path: string, data: unknown): Promise<T> {
    const res = await this.req.post(this.url + path, { data });
    expect(res.status(), `faux services ${path} : ${await res.text()}`).toBe(200);
    return (await res.json()) as T;
  }

  async state(): Promise<any> {
    const res = await this.req.get(this.url + "/__e2e/state");
    expect(res.status()).toBe(200);
    return res.json();
  }

  hold(role: string) {
    return this.post("/__e2e/hold", { role });
  }

  release(role: string) {
    return this.post("/__e2e/release", { role });
  }
}

/** Client API autonome (session administrateur partagée), utilisable dans beforeAll. */
export async function workerApi(playwright: { request: { newContext: (o: object) => Promise<APIRequestContext> } }) {
  const ctx = await playwright.request.newContext({ baseURL: readState().baseURL, storageState: AUTH_FILE });
  return { api: new Api(ctx), dispose: () => ctx.dispose() };
}

/** Attend la fin des chargements visibles (squelettes) de la page. */
export async function settled(page: Page) {
  await expect(page.locator(".animate-shimmer")).toHaveCount(0, { timeout: 15_000 });
}

type Fixtures = { e2e: E2EState; api: Api; fakes: Fakes };

export const test = base.extend<Fixtures>({
  e2e: async ({}, use) => use(readState()),
  baseURL: async ({ e2e }, use) => use(e2e.baseURL),
  api: async ({ page }, use) => use(new Api(page.request)),
  fakes: async ({ playwright, e2e }, use) => {
    const ctx = await playwright.request.newContext();
    await use(new Fakes(ctx, e2e.fakesURL));
    await ctx.dispose();
  },
});

export { expect };

/** Vérifie que l'élément a le focus et qu'un indicateur de focus est visible (outline ou anneau). */
export async function expectVisibleFocus(locator: Locator) {
  await expect(locator).toBeFocused();
  const visible = await locator.evaluate((el) => {
    const s = getComputedStyle(el);
    const outline = s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0;
    const ring = s.boxShadow !== "" && s.boxShadow !== "none";
    return outline || ring;
  });
  expect(visible, "indicateur de focus visible").toBe(true);
}

/** Appuie sur Tab jusqu'à ce que `target` ait le focus (au plus `max` fois). */
export async function tabTo(page: Page, target: Locator, max = 60) {
  for (let i = 0; i < max; i++) {
    if (await target.evaluate((el) => el === document.activeElement).catch(() => false)) return;
    await page.keyboard.press("Tab");
  }
  await expect(target).toBeFocused();
}
