import type { ReactNode } from "react";
import { AlertTriangle, Inbox, RefreshCw, WifiOff } from "lucide-react";
import { ApiError, errorMessage } from "@/api/client";
import { Button } from "./Button";
import { Skeleton } from "./primitives";
import { cn } from "@/lib/cn";

export function EmptyState({
  title,
  description,
  icon,
  action,
  className,
}: {
  title: string;
  description?: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-2 px-6 py-10 text-center", className)}>
      <div className="mb-1 rounded-full border border-border bg-panel p-2.5 text-fg-subtle" aria-hidden>
        {icon ?? <Inbox className="h-5 w-5" />}
      </div>
      <p className="text-sm font-medium text-fg">{title}</p>
      {description && <p className="max-w-sm text-xs text-fg-muted">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

export function ErrorState({ error, onRetry, className }: { error: unknown; onRetry?: () => void; className?: string }) {
  const network = error instanceof ApiError && (error.status === 0 || error.status === 503);
  return (
    <div role="alert" className={cn("flex flex-col items-center justify-center gap-2 px-6 py-10 text-center", className)}>
      <div className="mb-1 rounded-full border border-danger/30 bg-danger/10 p-2.5 text-danger" aria-hidden>
        {network ? <WifiOff className="h-5 w-5" /> : <AlertTriangle className="h-5 w-5" />}
      </div>
      <p className="text-sm font-medium text-fg">{network ? "Service indisponible" : "Impossible de charger les données"}</p>
      <p className="max-w-md text-xs text-fg-muted">
        {errorMessage(error)}
        {error instanceof ApiError && error.code && (
          <>
            {" "}
            <code className="font-mono text-fg-subtle">({error.code})</code>
          </>
        )}
      </p>
      {onRetry && (
        <Button size="sm" className="mt-2" onClick={onRetry} icon={<RefreshCw className="h-3.5 w-3.5" aria-hidden />}>
          Réessayer
        </Button>
      )}
    </div>
  );
}

/** Bandeau « mode dégradé » : données affichées potentiellement périmées. */
export function DegradedBanner({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div role="status" className="flex items-center gap-3 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
      <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden />
      <span className="flex-1">Données possiblement périmées : la dernière actualisation a échoué ({errorMessage(error)}).</span>
      {onRetry && (
        <button type="button" onClick={onRetry} className="focus-ring rounded font-medium underline underline-offset-2">
          Réessayer
        </button>
      )}
    </div>
  );
}

export function ListSkeleton({ rows = 5, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("space-y-2 p-4", className)} aria-busy="true" aria-label="Chargement">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-3">
          <Skeleton className="h-4 w-20" />
          <Skeleton className={cn("h-4 flex-1", i % 2 ? "max-w-[60%]" : "max-w-[80%]")} />
          <Skeleton className="h-4 w-16" />
        </div>
      ))}
    </div>
  );
}

/**
 * Rend loading / erreur / dégradé / vide / données de manière uniforme pour une requête.
 */
export function QueryView<T>({
  query,
  children,
  isEmpty,
  empty,
  skeleton,
}: {
  query: { data: T | undefined; error: unknown; isPending: boolean; isError: boolean; refetch: () => unknown };
  children: (data: T) => ReactNode;
  isEmpty?: (data: T) => boolean;
  empty?: ReactNode;
  skeleton?: ReactNode;
}) {
  if (query.isPending) return <>{skeleton ?? <ListSkeleton />}</>;
  if (query.data === undefined) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  return (
    <>
      {query.isError && (
        <div className="mb-3">
          <DegradedBanner error={query.error} onRetry={() => void query.refetch()} />
        </div>
      )}
      {isEmpty?.(query.data) ? empty ?? <EmptyState title="Rien à afficher" /> : children(query.data)}
    </>
  );
}
