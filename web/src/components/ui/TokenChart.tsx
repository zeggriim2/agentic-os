import { formatCompact, formatDay, formatNumber } from "@/lib/format";

/** Mini-histogramme SVG maison (sans dépendance) des tokens sur 7 jours. */
export function TokenChart({ data }: { data: { day: string; total: number }[] }) {
  const W = 280;
  const H = 72;
  const gap = 6;
  const n = Math.max(data.length, 1);
  const bw = (W - gap * (n - 1)) / n;
  const max = Math.max(1, ...data.map((d) => d.total));
  const total = data.reduce((s, d) => s + d.total, 0);
  const summary = data.map((d) => `${formatDay(d.day)} : ${formatNumber(d.total)}`).join(", ");
  return (
    <figure className="w-full">
      <svg
        viewBox={`0 0 ${W} ${H + 16}`}
        role="img"
        aria-label={`Tokens sur 7 jours, total ${formatNumber(total)}. ${summary}`}
        className="h-auto w-full overflow-visible"
      >
        <line x1={0} x2={W} y1={H} y2={H} className="stroke-border" strokeWidth={1} />
        {data.map((d, i) => {
          const h = d.total > 0 ? Math.max(2, (d.total / max) * (H - 4)) : 0;
          const x = i * (bw + gap);
          const last = i === data.length - 1;
          return (
            <g key={d.day}>
              <title>{`${formatDay(d.day)} — ${formatNumber(d.total)} tokens`}</title>
              <rect x={x} y={0} width={bw} height={H} className="fill-transparent" />
              <rect x={x} y={H - h} width={bw} height={h} rx={2} className={last ? "fill-accent" : "fill-accent/40"} />
              <text x={x + bw / 2} y={H + 12} textAnchor="middle" className="fill-fg-subtle text-[9px]">
                {formatDay(d.day)}
              </text>
            </g>
          );
        })}
      </svg>
      <figcaption className="sr-only">Maximum journalier : {formatCompact(max)} tokens</figcaption>
    </figure>
  );
}
