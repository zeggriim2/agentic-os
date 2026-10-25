import { useEffect, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { ApiError, apiRequest, onUnauthorized, setCsrfToken } from "@/api/client";
import { qk } from "@/api/queries";
import type { LoginRequest, LoginResponse, Me } from "@/api/types";

/** Session courante : null si non authentifié (401), lève pour les autres erreurs. */
export function useMe() {
  return useQuery<Me | null>({
    queryKey: qk.me,
    queryFn: async ({ signal }) => {
      try {
        const me = await apiRequest<Me>("/api/auth/me", { signal, skipUnauthorizedHandler: true });
        setCsrfToken(me.csrf_token);
        return me;
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          setCsrfToken(null);
          return null;
        }
        throw e;
      }
    },
    staleTime: 5 * 60_000,
    retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 2,
  });
}

/** Branche le 401 global : purge le cache et renvoie vers /login. */
export function useUnauthorizedRedirect() {
  const qc = useQueryClient();
  useEffect(() => {
    onUnauthorized(() => {
      qc.setQueryData(qk.me, null);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
    });
    return () => onUnauthorized(null);
  }, [qc]);
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: LoginRequest) =>
      apiRequest<LoginResponse>("/api/auth/login", { method: "POST", body, skipUnauthorizedHandler: true }),
    onSuccess: async (res, vars) => {
      setCsrfToken(res.csrf_token);
      qc.setQueryData<Me>(qk.me, { login: res.login ?? vars.login, csrf_token: res.csrf_token });
      await qc.invalidateQueries({ queryKey: qk.me });
    },
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiRequest("/api/auth/logout", { method: "POST", body: {}, skipUnauthorizedHandler: true }),
    onSettled: () => {
      setCsrfToken(null);
      // Ne pas utiliser qc.clear() : il retire la requête « me » observée par RequireAuth, qui ne
      // serait alors jamais notifiée du null (l'UI restait affichée après la déconnexion).
      qc.setQueryData(qk.me, null);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
    },
  });
}

export function FullScreenLoader({ label = "Chargement…" }: { label?: string }) {
  return (
    <div className="flex min-h-screen items-center justify-center gap-2 text-sm text-fg-muted" role="status">
      <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
      {label}
    </div>
  );
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const me = useMe();
  const loc = useLocation();
  if (me.isPending) return <FullScreenLoader label="Vérification de la session…" />;
  if (me.isError && me.data === undefined) {
    return (
      <div className="flex min-h-screen items-center justify-center p-6">
        <div className="card max-w-md p-6 text-center" role="alert">
          <p className="font-medium text-fg">Serveur injoignable</p>
          <p className="mt-1 text-sm text-fg-muted">{me.error instanceof Error ? me.error.message : "Erreur inconnue."}</p>
          <button type="button" className="focus-ring mt-4 rounded text-sm text-accent hover:underline" onClick={() => void me.refetch()}>
            Réessayer
          </button>
        </div>
      </div>
    );
  }
  if (!me.data) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />;
  return <>{children}</>;
}
