import { AlertTriangle, CheckCircle2, Download, ExternalLink as ExtIcon, FileText, Film, XCircle } from "lucide-react";
import { useTicketRecordings } from "@/api/queries";
import type { RecordingFile, RecordingSession } from "@/api/types";
import { Mono } from "@/components/ui/primitives";
import { ToneBadge } from "@/components/ui/StateBadge";
import { EmptyState, QueryView } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { formatBytes } from "@/lib/format";

export const fileURL = (f: RecordingFile) => `/api/recordings/${f.id}/file`;
/** Visualiseur de traces Playwright servi par Agentic OS (aucun service externe). */
export const traceViewerURL = (f: RecordingFile) => `/trace-viewer/index.html?trace=${encodeURIComponent(fileURL(f))}`;

function linkClass(primary = false) {
  return primary
    ? "focus-ring inline-flex items-center gap-1.5 rounded-md bg-fg px-2.5 py-1.5 text-xs font-medium text-bg hover:opacity-90"
    : "focus-ring inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-fg hover:bg-hover";
}

function Issues({ s }: { s: RecordingSession }) {
  const r = s.report;
  if (!r) return null;
  const failedSteps = (r.steps ?? []).filter((x) => !x.ok);
  const items = [
    ...(r.error ? [`Scénario : ${r.error}`] : []),
    ...failedSteps.map((x) => `Étape « ${x.name} » : ${x.error ?? "échec"}`),
    ...(r.console_errors ?? []).map((e) => `Erreur JS : ${e}`),
    ...(r.failed_requests ?? []).map((e) => `Requête : ${e}`),
  ];
  if (items.length === 0) return null;
  return (
    <div role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-fg" data-testid="recording-issues">
      <p className="flex items-center gap-1.5 font-medium">
        <AlertTriangle className="h-3.5 w-3.5 text-danger" aria-hidden />
        {items.length} problème(s) relevé(s) pendant la session
      </p>
      <ul className="mt-1 list-disc space-y-0.5 pl-5">
        {items.slice(0, 12).map((x, i) => (
          <li key={i} className="break-words">
            {x}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Session({ s, acceptance }: { s: RecordingSession; acceptance: string[] }) {
  const r = s.report;
  return (
    <section className="space-y-3 rounded-lg border border-border p-3" data-testid={`recording-${s.run_id}`} aria-label={`Session QA du run ${s.run_id}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Film className="h-4 w-4 text-fg-muted" aria-hidden />
          <span className="font-medium text-fg">Session QA · run #{s.run_id}</span>
          {r ? (
            <ToneBadge tone={r.ok ? "success" : "danger"}>{r.ok ? "Scénario réussi" : "Scénario en échec"}</ToneBadge>
          ) : (
            <ToneBadge tone="neutral">Sans rapport</ToneBadge>
          )}
          {r && <span className="text-xs text-fg-subtle">{Math.max(1, Math.round(r.duration_ms / 1000))} s</span>}
          <span className="text-xs text-fg-subtle">
            · <Time value={s.created_at} /> · suppression automatique <Time value={s.expires_at} />
          </span>
        </div>
        <div className="flex flex-wrap gap-2">
          {s.trace && (
            <>
              <a className={linkClass(true)} href={traceViewerURL(s.trace)} target="_blank" rel="noopener">
                Ouvrir la trace <ExtIcon className="h-3 w-3" aria-hidden />
              </a>
              <a className={linkClass()} href={fileURL(s.trace)} download>
                <Download className="h-3 w-3" aria-hidden /> Trace ({formatBytes(s.trace.size)})
              </a>
            </>
          )}
          {s.scenario && (
            <a className={linkClass()} href={fileURL(s.scenario)} download>
              <FileText className="h-3 w-3" aria-hidden /> Scénario
            </a>
          )}
        </div>
      </div>
      <Issues s={s} />
      {s.video && (
        <video
          className="w-full max-w-3xl rounded-md border border-border bg-black"
          controls
          preload="metadata"
          src={fileURL(s.video)}
          aria-label="Vidéo de la session QA"
        >
          Votre navigateur ne lit pas la vidéo : <a href={fileURL(s.video)}>télécharger</a>.
        </video>
      )}
      {s.shots.length > 0 && (
        <div>
          <p className="label-caps mb-2">Captures</p>
          <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {s.shots.map((f) => (
              <li key={f.id} className="overflow-hidden rounded-md border border-border">
                <a href={fileURL(f)} target="_blank" rel="noopener" className="focus-ring block">
                  <img src={fileURL(f)} alt={f.label || f.name} loading="lazy" className="h-40 w-full bg-panel object-cover object-top" />
                </a>
                <div className="space-y-1 p-2 text-xs">
                  <p className="font-medium text-fg">{f.label || f.name}</p>
                  {f.criterion ? (
                    <p className="text-fg-muted">
                      <ToneBadge tone="info">Critère {f.criterion}</ToneBadge> {acceptance[f.criterion - 1] ?? ""}
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
      {r && (r.steps ?? []).length > 0 && (
        <div>
          <p className="label-caps mb-1">Étapes</p>
          <ul className="space-y-0.5 text-xs">
            {(r.steps ?? []).map((x, i) => (
              <li key={i} className="flex items-center gap-1.5">
                {x.ok ? (
                  <CheckCircle2 className="h-3.5 w-3.5 text-success" aria-label="réussie" />
                ) : (
                  <XCircle className="h-3.5 w-3.5 text-danger" aria-label="en échec" />
                )}
                <span className="text-fg">{x.name}</span>
                <span className="text-fg-subtle">{(x.ms / 1000).toFixed(1)} s</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {s.logs.length > 0 && (
        <p className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
          Journaux :
          {s.logs.map((f) => (
            <a key={f.id} href={fileURL(f)} download className="underline underline-offset-2">
              <Mono>{f.name.replace("logs/", "")}</Mono>
            </a>
          ))}
          {r && r.blocked_external > 0 && <span>· {r.blocked_external} requête(s) externe(s) bloquée(s) (sans réseau)</span>}
        </p>
      )}
    </section>
  );
}

/** Onglet « Enregistrement QA » : sessions navigateur du QA (vidéo, captures par critère, trace). */
export function QARecordings({ ticketId, acceptance }: { ticketId: number; acceptance: string[] }) {
  const q = useTicketRecordings(ticketId);
  return (
    <QueryView
      query={q}
      isEmpty={(d) => d.length === 0}
      empty={
        <EmptyState
          icon={<Film className="h-5 w-5" />}
          title="Aucun enregistrement QA"
          description="Le QA enregistre une session navigateur quand « QA avec navigateur » est activé sur le projet et que le ticket touche l'interface. Les enregistrements sont conservés 15 jours."
        />
      }
    >
      {(d) => (
        <div className="space-y-4">
          {d.map((s) => (
            <Session key={s.run_id} s={s} acceptance={acceptance} />
          ))}
        </div>
      )}
    </QueryView>
  );
}
