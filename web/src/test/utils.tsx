import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { vi } from "vitest";
import { ToastProvider } from "@/components/ui/Toast";

export interface Call {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: unknown;
}

type Handler = (call: Call, n: number) => { status?: number; body?: unknown } | undefined;

/** Mock de fetch (tests uniquement) : routes "METHOD /path" → réponse JSON. */
export function mockFetch(routes: Record<string, Handler | { status?: number; body?: unknown }>) {
  const calls: Call[] = [];
  const counts: Record<string, number> = {};
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? "GET").toUpperCase();
    const headers = (init?.headers ?? {}) as Record<string, string>;
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    const call: Call = { method, url, headers, body };
    calls.push(call);
    const path = url.split("?")[0];
    const key = `${method} ${path}`;
    counts[key] = (counts[key] ?? 0) + 1;
    const h = routes[key] ?? routes[`${method} ${url}`];
    const res = typeof h === "function" ? h(call, counts[key]!) : h;
    if (!res) {
      return new Response(JSON.stringify({ error: { code: "not_found", message: `non mocké: ${key}` } }), { status: 404 });
    }
    return new Response(res.body === undefined ? "" : JSON.stringify(res.body), {
      status: res.status ?? 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fn);
  return { fn, calls };
}

export function renderWithProviders(ui: ReactElement, { route = "/", path = "/" }: { route?: string; path?: string } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <Routes>
            <Route path={path} element={ui} />
            <Route path="*" element={<div data-testid="location">autre page</div>} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, client };
}
