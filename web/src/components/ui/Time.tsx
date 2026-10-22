import { useEffect, useState } from "react";
import { formatParis, formatRelative } from "@/lib/format";

/** Date relative (« il y a 3 min ») avec date absolue Europe/Paris en infobulle. */
export function Time({ value, className }: { value: string | null | undefined; className?: string }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);
  if (!value) return <span className={className}>—</span>;
  const abs = formatParis(value);
  return (
    <time dateTime={value} title={`${abs} (heure de Paris)`} className={className}>
      {formatRelative(value)}
    </time>
  );
}
