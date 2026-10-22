import { useEffect, useState, type ReactNode } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import {
  Activity,
  Bot,
  CheckSquare,
  FolderGit2,
  HeartPulse,
  KanbanSquare,
  LayoutDashboard,
  Layers,
  LogOut,
  Menu,
  Moon,
  ScrollText,
  Sun,
  X,
} from "lucide-react";
import { EventStreamProvider, useStreamStatus } from "@/api/events";
import { useOverview } from "@/api/queries";
import { useLogout, useMe, useUnauthorizedRedirect } from "@/auth/auth";
import { cn } from "@/lib/cn";
import { useTheme } from "@/lib/theme";
import { Logo } from "./Logo";

interface NavItem {
  to: string;
  label: string;
  icon: typeof Activity;
  end?: boolean;
  badge?: "approvals";
}

const NAV: { section?: string; items: NavItem[] }[] = [
  {
    items: [
      { to: "/", label: "Vue d'ensemble", icon: LayoutDashboard, end: true },
      { to: "/approvals", label: "Approbations", icon: CheckSquare, badge: "approvals" },
      { to: "/pipeline", label: "Pipeline", icon: KanbanSquare },
      { to: "/features", label: "Features", icon: Layers },
      { to: "/runs", label: "Runs", icon: Activity },
    ],
  },
  {
    section: "Configuration",
    items: [
      { to: "/agents", label: "Agents", icon: Bot },
      { to: "/projects", label: "Projets", icon: FolderGit2 },
    ],
  },
  {
    section: "Système",
    items: [
      { to: "/audit", label: "Audit", icon: ScrollText },
      { to: "/health", label: "Santé", icon: HeartPulse },
    ],
  },
];

function LiveIndicator({ compact }: { compact?: boolean }) {
  const s = useStreamStatus();
  const label = s === "connected" ? "Temps réel connecté" : s === "connecting" ? "Connexion temps réel…" : "Temps réel déconnecté";
  return (
    <div role="status" aria-live="polite" className="flex items-center gap-2 text-xs text-fg-muted" title={label}>
      <span className="relative flex h-2 w-2" aria-hidden>
        {s === "connected" && <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-50" />}
        <span
          className={cn(
            "relative inline-flex h-2 w-2 rounded-full",
            s === "connected" ? "bg-success" : s === "connecting" ? "bg-review" : "bg-danger",
          )}
        />
      </span>
      <span className={compact ? "sr-only" : undefined}>
        {s === "connected" ? "Temps réel" : s === "connecting" ? "Connexion…" : "Déconnecté"}
      </span>
    </div>
  );
}

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  const overview = useOverview();
  const pending = overview.data?.pending_approvals ?? 0;
  return (
    <nav aria-label="Navigation principale" className="space-y-5">
      {NAV.map((group, gi) => (
        <div key={gi}>
          {group.section && <p className="label-caps mb-1.5 px-2.5">{group.section}</p>}
          <ul className="space-y-0.5">
            {group.items.map((it) => (
              <li key={it.to}>
                <NavLink
                  to={it.to}
                  end={it.end}
                  onClick={onNavigate}
                  className={({ isActive }) =>
                    cn(
                      "focus-ring group flex h-8 items-center gap-2.5 rounded-md px-2.5 text-sm font-medium transition-colors",
                      isActive ? "bg-hover text-fg" : "text-fg-muted hover:bg-hover/60 hover:text-fg",
                    )
                  }
                >
                  <it.icon className="h-4 w-4 shrink-0" aria-hidden />
                  <span className="flex-1 truncate">{it.label}</span>
                  {it.badge === "approvals" && pending > 0 && (
                    <span className="rounded-full bg-review/15 px-1.5 text-2xs font-semibold tabular-nums text-review" aria-label={`${pending} en attente`}>
                      {pending}
                    </span>
                  )}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function SidebarFooter() {
  const me = useMe();
  const logout = useLogout();
  const [theme, toggle] = useTheme();
  return (
    <div className="space-y-3 border-t border-border pt-3">
      <div className="flex items-center justify-between px-2.5">
        <LiveIndicator />
        <button
          type="button"
          onClick={toggle}
          className="focus-ring rounded-md p-1.5 text-fg-muted hover:bg-hover hover:text-fg"
          aria-label={theme === "dark" ? "Passer au thème clair" : "Passer au thème sombre"}
          title={theme === "dark" ? "Thème clair" : "Thème sombre"}
        >
          {theme === "dark" ? <Sun className="h-4 w-4" aria-hidden /> : <Moon className="h-4 w-4" aria-hidden />}
        </button>
      </div>
      <div className="flex items-center justify-between gap-2 px-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent-solid text-2xs font-semibold uppercase text-accent-fg" aria-hidden>
            {me.data?.login.slice(0, 1) ?? "?"}
          </span>
          <span className="truncate text-sm text-fg">{me.data?.login}</span>
        </div>
        <button
          type="button"
          onClick={() => logout.mutate()}
          className="focus-ring rounded-md p-1.5 text-fg-muted hover:bg-hover hover:text-fg"
          aria-label="Se déconnecter"
          title="Se déconnecter"
        >
          <LogOut className="h-4 w-4" aria-hidden />
        </button>
      </div>
    </div>
  );
}

function Brand() {
  return (
    <div className="flex items-center gap-2 px-2.5">
      <Logo className="h-6 w-6" />
      <span className="text-sm font-semibold tracking-tight text-fg">Agentic OS</span>
    </div>
  );
}

function MobileDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-40 lg:hidden">
      <div className="absolute inset-0 bg-black/60" aria-hidden onClick={onClose} />
      <div role="dialog" aria-modal="true" aria-label="Menu" className="absolute inset-y-0 left-0 flex w-72 animate-slide-in flex-col gap-5 border-r border-border bg-panel p-3">
        <div className="flex items-center justify-between">
          <Brand />
          <button type="button" onClick={onClose} className="focus-ring rounded-md p-2 text-fg-muted hover:bg-hover" aria-label="Fermer le menu" autoFocus>
            <X className="h-4 w-4" aria-hidden />
          </button>
        </div>
        <div className="scroll-thin flex-1 overflow-y-auto">
          <NavList onNavigate={onClose} />
        </div>
        <SidebarFooter />
      </div>
    </div>
  );
}

function Shell({ children }: { children?: ReactNode }) {
  const [open, setOpen] = useState(false);
  const loc = useLocation();
  useEffect(() => setOpen(false), [loc.pathname]);
  return (
    <div className="min-h-screen bg-bg lg:grid lg:grid-cols-[232px_1fr]">
      <a href="#main" className="focus-ring sr-only z-50 rounded bg-accent-solid px-3 py-2 text-accent-fg focus:not-sr-only focus:absolute focus:left-3 focus:top-3">
        Aller au contenu
      </a>
      <aside className="sticky top-0 hidden h-screen flex-col gap-5 border-r border-border bg-panel p-3 lg:flex">
        <div className="pt-1">
          <Brand />
        </div>
        <div className="scroll-thin flex-1 overflow-y-auto">
          <NavList />
        </div>
        <SidebarFooter />
      </aside>
      <div className="sticky top-0 z-30 flex h-12 items-center justify-between border-b border-border bg-panel/95 px-3 backdrop-blur lg:hidden">
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="focus-ring rounded-md p-2 text-fg-muted hover:bg-hover hover:text-fg"
          aria-label="Ouvrir le menu"
          aria-expanded={open}
        >
          <Menu className="h-5 w-5" aria-hidden />
        </button>
        <Brand />
        <div className="px-2">
          <LiveIndicator compact />
        </div>
      </div>
      <MobileDrawer open={open} onClose={() => setOpen(false)} />
      <main id="main" tabIndex={-1} className="min-w-0 px-4 py-5 outline-none sm:px-6 lg:px-8 lg:py-7">
        <div className="mx-auto max-w-[1400px]">{children ?? <Outlet />}</div>
      </main>
    </div>
  );
}

export function AppShell() {
  useUnauthorizedRedirect();
  const me = useMe();
  return (
    <EventStreamProvider enabled={Boolean(me.data)}>
      <Shell />
    </EventStreamProvider>
  );
}
