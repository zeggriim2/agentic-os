import { useCallback, useSyncExternalStore } from "react";

export type Theme = "dark" | "light";
const KEY = "aos-theme";
const subs = new Set<() => void>();

export function readTheme(): Theme {
  try {
    return localStorage.getItem(KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

let current: Theme = typeof window === "undefined" ? "dark" : readTheme();

export function applyTheme(t: Theme): void {
  document.documentElement.classList.toggle("dark", t === "dark");
}

export function setTheme(t: Theme): void {
  current = t;
  applyTheme(t);
  try {
    localStorage.setItem(KEY, t);
  } catch {
    /* stockage indisponible */
  }
  subs.forEach((f) => f());
}

function subscribe(f: () => void) {
  subs.add(f);
  return () => {
    subs.delete(f);
  };
}

/** Thème global persistant (sombre par défaut), partagé par tous les composants. */
export function useTheme(): [Theme, () => void] {
  const theme = useSyncExternalStore(subscribe, () => current, () => "dark" as Theme);
  const toggle = useCallback(() => setTheme(current === "dark" ? "light" : "dark"), []);
  return [theme, toggle];
}
