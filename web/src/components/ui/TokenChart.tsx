import { formatCompact, formatDay, formatNumber } from "@/lib/format";

type Day = { day: string; total: number; fresh_input?: number; cache_read?: number; output?: number };

/** Part « nouvelle » (entrée nouvelle + sortie) et part relue du cache ; sans répartition, tout est « nouveau ». */
export function daySplit(d: Day): { fresh: number; cache: number } {
  if (d.fresh_input === undefined || d.cache_read === undefined || d.output === undefined) return { fresh: d.total, cache: 0 };
  return { fresh: d.fresh_input + d.output, cache: d.cache_read };
}

/** Mini-histogramme SVG empilé (sans dépendance) des tokens sur 7 jours : nouveaux en plein, cache en clair. */
export function TokenChart({ data }: { data: Day[] }) {
  const W = 280;
  const H = 72;
  const gap = 6;
  const n = Math.max(data.length, 1);
  const bw = (W - gap * (n - 1)) / n;
  const split = data.map(daySplit);
  const max = Math.max(1, ...split.map((s) => s.fresh + s.cache));
  const fresh = split.reduce((a, s) => a + s.fresh, 0);
  const cache = split.reduce((a, s) => a + s.cache, 0);
  const summary = data.map((d, i) => `${formatDay(d.day)} : ${formatNumber(split[i]!.fresh)} nouveaux, ${formatNumber(split[i]!.cache)} cache`).join(", ");
  const scale = (v: number) => (v > 0 ? Math.max(2, (v / max) * (H - 4)) : 0);
  return (
    <figure className="w-full">
      <svg
        viewBox={`0 0 ${W} ${H + 16}`}
        role="img"
        aria-label={`Tokens sur 7 jours : ${formatNumber(fresh)} nouveaux et ${formatNumber(cache)} relus du cache. ${summary}`}
        className="h-auto w-full overflow-visible"
      >
        <line x1={0} x2={W} y1={H} y2={H} className="stroke-border" strokeWidth={1} />
        {data.map((d, i) => {
          const s = split[i]!;
          const hf = scale(s.fresh);
          const hc = s.cache > 0 ? Math.max(1, (s.cache / max) * (H - 4)) : 0;
          const x = i * (bw + gap);
          const last = i === data.length - 1;
          return (
            <g key={d.day} data-testid="token-day">
              <title>{`${formatDay(d.day)} — ${formatNumber(s.fresh)} nouveaux, ${formatNumber(s.cache)} relus du cache`}</title>
              <rect x={x} y={0} width={bw} height={H} className="fill-transparent" />
              {hc > 0 && <rect x={x} y={H - hf - hc} width={bw} height={hc} rx={2} className="fill-accent/20" data-part="cache" />}
              {hf > 0 && <rect x={x} y={H - hf} width={bw} height={hf} rx={2} className={last ? "fill-accent" : "fill-accent/60"} data-part="fresh" />}
              <text x={x + bw / 2} y={H + 12} textAnchor="middle" className="fill-fg-subtle text-[9px]">
                {formatDay(d.day)}
              </text>
            </g>
          );
        })}
      </svg>
      <figcaption className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-2xs text-fg-subtle">
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-2 rounded-sm bg-accent" aria-hidden />
          nouveaux (entrée + sortie) : {formatCompact(fresh)}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2 w-2 rounded-sm bg-accent/20" aria-hidden />
          relus du cache : {formatCompact(cache)}
        </span>
      </figcaption>
    </figure>
  );
}
