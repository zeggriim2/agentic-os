export const PARIS_TZ = "Europe/Paris";

const absFmt = new Intl.DateTimeFormat("fr-FR", {
  timeZone: PARIS_TZ,
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
const shortFmt = new Intl.DateTimeFormat("fr-FR", {
  timeZone: PARIS_TZ,
  day: "2-digit",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
});
const timeFmt = new Intl.DateTimeFormat("fr-FR", { timeZone: PARIS_TZ, hour: "2-digit", minute: "2-digit", second: "2-digit" });
const dayFmt = new Intl.DateTimeFormat("fr-FR", { timeZone: "UTC", weekday: "short", day: "numeric" });
const rtf = new Intl.RelativeTimeFormat("fr", { numeric: "auto" });
const numFmt = new Intl.NumberFormat("fr-FR");
const compactFmt = new Intl.NumberFormat("fr-FR", { notation: "compact", maximumFractionDigits: 1 });

function toDate(v: string | Date | null | undefined): Date | null {
  if (v === null || v === undefined || v === "") return null;
  const d = v instanceof Date ? v : new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Date + heure absolues à Paris : « 21/10/2026 14:32:05 ». */
export function formatParis(v: string | Date | null | undefined): string {
  const d = toDate(v);
  return d ? absFmt.format(d).replace(",", "") : "—";
}
/** Format compact à Paris : « 21 oct. 14:32 ». */
export function formatParisShort(v: string | Date | null | undefined): string {
  const d = toDate(v);
  return d ? shortFmt.format(d).replace(",", "") : "—";
}
export function formatParisTime(v: string | Date | null | undefined): string {
  const d = toDate(v);
  return d ? timeFmt.format(d) : "—";
}

/** « il y a 5 minutes », « dans 2 heures », « hier »… */
export function formatRelative(v: string | Date | null | undefined, now: Date = new Date()): string {
  const d = toDate(v);
  if (!d) return "—";
  const diffSec = Math.round((d.getTime() - now.getTime()) / 1000);
  const abs = Math.abs(diffSec);
  if (abs < 45) return "à l'instant";
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["minute", 60],
    ["hour", 3600],
    ["day", 86400],
    ["week", 604800],
    ["month", 2629800],
    ["year", 31557600],
  ];
  let chosen: [Intl.RelativeTimeFormatUnit, number] = units[0]!;
  for (const u of units) if (abs >= u[1] * 0.9) chosen = u;
  return rtf.format(Math.round(diffSec / chosen[1]), chosen[0]);
}

/** Libellé d'un jour « YYYY-MM-DD » (sans décalage de fuseau). */
export function formatDay(day: string): string {
  const d = toDate(day.length === 10 ? `${day}T12:00:00Z` : day);
  return d ? dayFmt.format(d) : day;
}

export function formatNumber(n: number | null | undefined): string {
  return typeof n === "number" && Number.isFinite(n) ? numFmt.format(n) : "—";
}
export function formatCompact(n: number | null | undefined): string {
  return typeof n === "number" && Number.isFinite(n) ? compactFmt.format(n) : "—";
}

export function formatDuration(start: string | null | undefined, end?: string | null, now: Date = new Date()): string {
  const s = toDate(start);
  if (!s) return "—";
  const e = toDate(end) ?? now;
  let sec = Math.max(0, Math.round((e.getTime() - s.getTime()) / 1000));
  const h = Math.floor(sec / 3600);
  sec -= h * 3600;
  const m = Math.floor(sec / 60);
  sec -= m * 60;
  if (h) return `${h} h ${String(m).padStart(2, "0")} min`;
  if (m) return `${m} min ${String(sec).padStart(2, "0")} s`;
  return `${sec} s`;
}

export function shortSha(sha: string | null | undefined): string {
  return sha ? sha.slice(0, 7) : "—";
}

/** Taille lisible (Ko/Mo, base 1024). */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} o`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} Ko`;
  return `${(n / (1024 * 1024)).toFixed(1).replace(".", ",")} Mo`;
}
