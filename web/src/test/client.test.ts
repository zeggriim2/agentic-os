import { describe, expect, it, vi } from "vitest";
import { ApiError, apiRequest, buildQuery, onUnauthorized, setCsrfToken } from "@/api/client";
import { asPage, flattenDetail } from "@/api/queries";
import { mockFetch } from "./utils";

describe("client API", () => {
  it("envoie X-CSRF-Token sur les requêtes non-GET uniquement", async () => {
    const { calls } = mockFetch({
      "GET /api/overview": { body: { ok: true } },
      "POST /api/system/dispatch": { body: { ok: true } },
      "PATCH /api/agents/qa": { body: { ok: true } },
    });
    setCsrfToken("tok-123");
    await apiRequest("/api/overview");
    await apiRequest("/api/system/dispatch", { method: "POST", body: { paused: true } });
    await apiRequest("/api/agents/qa", { method: "PATCH", body: { enabled: false } });
    expect(calls[0]!.headers["X-CSRF-Token"]).toBeUndefined();
    expect(calls[1]!.headers["X-CSRF-Token"]).toBe("tok-123");
    expect(calls[1]!.headers["Content-Type"]).toBe("application/json");
    expect(calls[1]!.body).toEqual({ paused: true });
    expect(calls[2]!.headers["X-CSRF-Token"]).toBe("tok-123");
  });

  it("normalise le format d'erreur {error:{code,message}}", async () => {
    mockFetch({ "POST /api/approvals/1/decision": { status: 409, body: { error: { code: "already_decided", message: "Déjà décidée." } } } });
    const err = await apiRequest("/api/approvals/1/decision", { method: "POST", body: {} }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, code: "already_decided", message: "Déjà décidée." });
  });

  it("produit une erreur lisible si le corps n'est pas au format attendu", async () => {
    mockFetch({ "GET /api/x": { status: 503, body: "boom" } });
    const err = (await apiRequest("/api/x").catch((e) => e)) as ApiError;
    expect(err.status).toBe(503);
    expect(err.code).toBe("http_503");
    expect(err.message).toMatch(/indisponible/);
  });

  it("appelle le handler global sur 401 et purge le jeton CSRF", async () => {
    mockFetch({ "GET /api/overview": { status: 401, body: { error: { code: "unauthenticated", message: "Non authentifié." } } } });
    const handler = vi.fn();
    onUnauthorized(handler);
    setCsrfToken("t");
    await expect(apiRequest("/api/overview")).rejects.toMatchObject({ status: 401 });
    expect(handler).toHaveBeenCalledTimes(1);

    // Et plus de jeton sur la requête suivante
    const { calls } = mockFetch({ "POST /api/x": { body: {} } });
    await apiRequest("/api/x", { method: "POST", body: {} });
    expect(calls[0]!.headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("n'appelle pas le handler 401 quand skipUnauthorizedHandler est demandé (login)", async () => {
    mockFetch({ "POST /api/auth/login": { status: 401, body: { error: { code: "invalid_credentials", message: "Identifiants invalides." } } } });
    const handler = vi.fn();
    onUnauthorized(handler);
    await expect(apiRequest("/api/auth/login", { method: "POST", body: {}, skipUnauthorizedHandler: true })).rejects.toMatchObject({
      code: "invalid_credentials",
    });
    expect(handler).not.toHaveBeenCalled();
  });

  it("signale une erreur réseau", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))));
    await expect(apiRequest("/api/overview")).rejects.toMatchObject({ status: 0, code: "network_error" });
  });

  it("construit les query strings en ignorant les valeurs vides", () => {
    expect(buildQuery({ status: "running", role: "", project_id: undefined, limit: 50 })).toBe("?status=running&limit=50");
    expect(buildQuery({})).toBe("");
  });

  it("accepte les listes paginées et les détails imbriqués", () => {
    expect(asPage({ items: [1, 2], next_cursor: "c" })).toEqual({ items: [1, 2], next_cursor: "c" });
    expect(asPage([1])).toEqual({ items: [1], next_cursor: null });
    expect(flattenDetail<{ id: number; tickets: number[] }>({ feature: { id: 3 }, tickets: [1] }, "feature")).toEqual({ id: 3, tickets: [1] });
    expect(flattenDetail({ id: 3 }, "feature")).toEqual({ id: 3 });
  });
});
