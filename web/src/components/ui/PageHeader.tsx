import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";

export function PageHeader({
  title,
  description,
  actions,
  breadcrumbs,
  meta,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumbs?: { label: string; to: string }[];
  meta?: ReactNode;
}) {
  return (
    <header className="mb-5 space-y-2">
      {breadcrumbs && breadcrumbs.length > 0 && (
        <nav aria-label="Fil d'Ariane" className="flex items-center gap-1 text-xs text-fg-subtle">
          {breadcrumbs.map((b) => (
            <span key={b.to} className="flex items-center gap-1">
              <Link to={b.to} className="focus-ring rounded hover:text-fg">
                {b.label}
              </Link>
              <ChevronRight className="h-3 w-3" aria-hidden />
            </span>
          ))}
        </nav>
      )}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight text-fg">{title}</h1>
          {description && <p className="mt-1 text-sm text-fg-muted">{description}</p>}
          {meta && <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-fg-muted">{meta}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </header>
  );
}
