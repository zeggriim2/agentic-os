import type { ApiErrorBody } from "./types";

/** Erreur normalisée renvoyée par le client (format §9 : {"error":{"code","message"}}). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

let csrfToken: string | null = null;
let unauthorizedHandler: (() => void) | null = null;

export function setCsrfToken(token: string | null): void {
  csrfToken = token;
}
export function getCsrfToken(): string | null {
  return csrfToken;
}
/** Appelé à chaque 401 (hors tentative de login) : l'application renvoie vers /login. */
export function onUnauthorized(handler: (() => void) | null): void {
  unauthorizedHandler = handler;
}

const DEFAULT_MESSAGES: Record<number, string> = {
  400: "Requête invalide.",
  401: "Session expirée ou absente. Veuillez vous reconnecter.",
  403: "Action refusée.",
  404: "Ressource introuvable.",
  409: "Conflit : la ressource a changé entre-temps.",
  422: "Données refusées par le serveur.",
  429: "Trop de tentatives. Réessayez dans une minute.",
  503: "Service indisponible.",
};

function isErrorBody(v: unknown): v is ApiErrorBody {
  if (typeof v !== "object" || v === null) return false;
  const e = (v as { error?: unknown }).error;
  return typeof e === "object" && e !== null && typeof (e as { code?: unknown }).code === "string";
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
  /** Ne pas déclencher le handler global sur 401 (ex. login, /me). */
  skipUnauthorizedHandler?: boolean;
}

export type QueryValue = string | number | boolean | null | undefined;

export function buildQuery(params: Record<string, QueryValue> = {}): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export async function apiRequest<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const headers: Record<string, string> = { Accept: "application/json" };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET") {
    if (csrfToken) headers["X-CSRF-Token"] = csrfToken;
  }

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      credentials: "same-origin",
      signal: opts.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(0, "network_error", "Serveur injoignable. Vérifiez le tunnel SSH et le service.");
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }

  if (!res.ok) {
    if (res.status === 401 && !opts.skipUnauthorizedHandler) {
      csrfToken = null;
      unauthorizedHandler?.();
    }
    if (isErrorBody(data)) throw new ApiError(res.status, data.error.code, data.error.message);
    throw new ApiError(
      res.status,
      `http_${res.status}`,
      DEFAULT_MESSAGES[res.status] ?? `Erreur inattendue (HTTP ${res.status}).`,
    );
  }
  if (text && data === undefined) {
    throw new ApiError(res.status, "invalid_json", "Réponse du serveur illisible (JSON invalide).");
  }
  return data as T;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => apiRequest<T>(path, { signal }),
  post: <T>(path: string, body?: unknown) => apiRequest<T>(path, { method: "POST", body: body ?? {} }),
  patch: <T>(path: string, body: unknown) => apiRequest<T>(path, { method: "PATCH", body }),
};

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return "Erreur inconnue.";
}
