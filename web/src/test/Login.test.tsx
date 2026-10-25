import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { getCsrfToken } from "@/api/client";
import LoginPage from "@/pages/Login";
import { mockFetch, renderWithProviders } from "./utils";
import { Route, Routes, MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { ToastProvider } from "@/components/ui/Toast";

function renderLogin(from?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter initialEntries={[{ pathname: "/login", state: from ? { from } : null }]}>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<div>Accueil</div>} />
            <Route path="/runs/5" element={<div>Run 5</div>} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("Login", () => {
  it("valide les champs requis sans appeler l'API", async () => {
    const { calls } = mockFetch({ "GET /api/auth/me": { status: 401, body: { error: { code: "unauthenticated", message: "x" } } } });
    renderLogin();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Se connecter" }));
    expect(screen.getByText("L'identifiant est requis.")).toBeInTheDocument();
    expect(screen.getByText("Le mot de passe est requis.")).toBeInTheDocument();
    expect(calls.some((c) => c.url === "/api/auth/login")).toBe(false);
  });

  it("se connecte, mémorise le jeton CSRF et revient à la page demandée", async () => {
    let meCalls = 0;
    const { calls } = mockFetch({
      "GET /api/auth/me": () =>
        ++meCalls === 1 ? { status: 401, body: { error: { code: "unauthenticated", message: "x" } } } : { body: { login: "admin", csrf_token: "csrf-1" } },
      "POST /api/auth/login": { body: { csrf_token: "csrf-1" } },
    });
    renderLogin("/runs/5");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/Identifiant/), "admin");
    await user.type(screen.getByLabelText(/Mot de passe/), "secret");
    await user.click(screen.getByRole("button", { name: "Se connecter" }));
    expect(await screen.findByText("Run 5")).toBeInTheDocument();
    const login = calls.find((c) => c.url === "/api/auth/login")!;
    expect(login.method).toBe("POST");
    expect(login.body).toEqual({ login: "admin", password: "secret" });
    expect(getCsrfToken()).toBe("csrf-1");
  });

  it("affiche le message d'erreur du serveur", async () => {
    mockFetch({
      "GET /api/auth/me": { status: 401, body: { error: { code: "unauthenticated", message: "x" } } },
      "POST /api/auth/login": { status: 401, body: { error: { code: "invalid_credentials", message: "Identifiants invalides." } } },
    });
    renderLogin();
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/Identifiant/), "admin");
    await user.type(screen.getByLabelText(/Mot de passe/), "mauvais");
    await user.click(screen.getByRole("button", { name: "Se connecter" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Identifiants invalides.");
    expect(screen.getByRole("button", { name: "Se connecter" })).toBeInTheDocument();
  });

  it("redirige vers /login quand la session est absente (garde de route)", async () => {
    const { RequireAuth } = await import("@/auth/auth");
    mockFetch({ "GET /api/auth/me": { status: 401, body: { error: { code: "unauthenticated", message: "x" } } } });
    renderWithProviders(
      <RequireAuth>
        <div>Privé</div>
      </RequireAuth>,
      { route: "/", path: "/" },
    );
    expect(await screen.findByTestId("location")).toBeInTheDocument();
    expect(screen.queryByText("Privé")).not.toBeInTheDocument();
  });
});
