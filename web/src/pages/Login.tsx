import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { AlertCircle, Moon, Sun } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useLogin, useMe } from "@/auth/auth";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/primitives";
import { Logo } from "@/components/layout/Logo";
import { useTheme } from "@/lib/theme";

export default function LoginPage() {
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [touched, setTouched] = useState(false);
  const mutation = useLogin();
  const me = useMe();
  const nav = useNavigate();
  const loc = useLocation();
  const [theme, toggleTheme] = useTheme();
  const from = (loc.state as { from?: string } | null)?.from ?? "/";

  if (me.data) return <Navigate to={from === "/login" ? "/" : from} replace />;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!login.trim() || !password) return;
    mutation.mutate(
      { login: login.trim(), password },
      { onSuccess: () => nav(from === "/login" ? "/" : from, { replace: true }) },
    );
  };

  return (
    <main className="relative flex min-h-screen items-center justify-center bg-bg px-4">
      <button
        type="button"
        onClick={toggleTheme}
        className="focus-ring absolute right-4 top-4 rounded-md p-2 text-fg-muted hover:bg-hover hover:text-fg"
        aria-label={theme === "dark" ? "Passer au thème clair" : "Passer au thème sombre"}
      >
        {theme === "dark" ? <Sun className="h-4 w-4" aria-hidden /> : <Moon className="h-4 w-4" aria-hidden />}
      </button>
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <Logo className="h-10 w-10" />
          <div>
            <h1 className="text-xl font-semibold tracking-tight text-fg">Agentic OS</h1>
            <p className="mt-1 text-sm text-fg-muted">Connectez-vous pour piloter vos agents.</p>
          </div>
        </div>
        <form onSubmit={submit} noValidate className="card space-y-4 p-6" aria-label="Connexion">
          {mutation.isError && (
            <div role="alert" className="flex items-start gap-2 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
              <span>{errorMessage(mutation.error)}</span>
            </div>
          )}
          <Field label="Identifiant" required error={touched && !login.trim() ? "L'identifiant est requis." : undefined}>
            {(p) => (
              <Input {...p} name="login" autoComplete="username" autoFocus value={login} onChange={(e) => setLogin(e.target.value)} />
            )}
          </Field>
          <Field label="Mot de passe" required error={touched && !password ? "Le mot de passe est requis." : undefined}>
            {(p) => (
              <Input
                {...p}
                name="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            )}
          </Field>
          <Button type="submit" variant="primary" className="w-full justify-center" loading={mutation.isPending}>
            Se connecter
          </Button>
        </form>
        <p className="mt-6 text-center text-xs text-fg-subtle">Accès local uniquement (127.0.0.1 via tunnel SSH).</p>
      </div>
    </main>
  );
}
