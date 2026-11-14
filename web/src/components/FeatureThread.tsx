import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { MessageSquareWarning, Send } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useFeatureThread, usePostMessage } from "@/api/queries";
import type { Approval, FeatureMessage, FeatureThread as Thread, ThreadRun } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Select, Skeleton, Textarea } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { EmptyState, ErrorState } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { formatNumber } from "@/lib/format";
import { APPROVAL_KIND_LABELS, roleLabel } from "@/lib/states";
import { cn } from "@/lib/cn";

/** Rôles auxquels on peut écrire, dans l'ordre du pipeline. */
export const CHAT_ROLES = ["po", "tech_lead", "developer", "reviewer", "qa"] as const;
const AVATAR: Record<string, string> = { po: "PO", tech_lead: "TL", developer: "DEV", reviewer: "REV", qa: "QA" };
export const MAX_MESSAGE = 4000;

type Kind = "instruction" | "question";

/** Préremplissage du dialogue « Demander une correction » depuis une réponse d'agent. */
export interface CorrectionDraft {
  title: string;
  description: string;
}

type Item =
  | { at: string; seq: number; type: "run"; run: ThreadRun }
  | { at: string; seq: number; type: "approval"; approval: Approval }
  | { at: string; seq: number; type: "message"; message: FeatureMessage };

/** Fil chronologique : passages d'agents, décisions et messages (les réponses aux questions sont des messages). */
export function threadItems(t: Thread): Item[] {
  const items: Item[] = [
    ...t.runs.filter((r) => r.job_kind !== "answer_question").map((run, i) => ({ at: run.started_at, seq: i, type: "run" as const, run })),
    ...t.approvals.map((approval, i) => ({ at: approval.created_at, seq: i, type: "approval" as const, approval })),
    ...t.messages.map((message, i) => ({ at: message.created_at, seq: i, type: "message" as const, message })),
  ];
  return items.sort((a, b) => a.at.localeCompare(b.at) || a.seq - b.seq);
}

function login(author: string) {
  return author.startsWith("user:") ? author.slice(5) : author;
}

function Avatar({ role, me }: { role?: string | null; me?: string }) {
  return (
    <div
      className={cn(
        "flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-[10px] font-bold",
        me ? "bg-review/20 text-review" : "bg-progress/20 text-progress",
      )}
      aria-hidden
    >
      {me ? me.slice(0, 2).toUpperCase() : (AVATAR[role ?? ""] ?? "AG")}
    </div>
  );
}

function Bubble({ mine, avatar, head, children, testid }: { mine?: boolean; avatar: ReactNode; head: ReactNode; children: ReactNode; testid?: string }) {
  return (
    <li className={cn("flex gap-2.5", mine && "flex-row-reverse")} data-testid={testid}>
      {avatar}
      <div className={cn("min-w-0 max-w-[85%]", mine && "text-right")}>
        <div className={cn("mb-0.5 flex flex-wrap items-baseline gap-x-2 text-xs text-fg-subtle", mine && "justify-end")}>{head}</div>
        <div className={cn("rounded-lg border px-3 py-2 text-left text-sm", mine ? "border-review/30 bg-review/10 text-fg" : "border-border bg-panel text-fg")}>
          {children}
        </div>
      </div>
    </li>
  );
}

function RunItem({ run }: { run: ThreadRun }) {
  const working = run.status === "running" || run.status === "submitted";
  return (
    <Bubble
      testid={`thread-run-${run.id}`}
      avatar={<Avatar role={run.agent_role} />}
      head={
        <>
          <b className="text-fg">{roleLabel(run.agent_role)}</b>
          {run.ticket_key && <span className="font-mono">{run.ticket_key}</span>}
          <Time value={run.started_at} />
        </>
      }
    >
      {working ? (
        <p className="flex items-center gap-2 text-progress">
          <span className="h-2 w-2 animate-pulse rounded-full bg-progress" aria-hidden />
          Au travail…
          <Link to={`/runs/${run.id}`} className="focus-ring rounded text-xs underline">
            suivre en direct
          </Link>
        </p>
      ) : (
        <>
          {run.summary ? <p className="whitespace-pre-wrap">{run.summary}</p> : <p className="text-fg-muted">{run.error ?? "Passage terminé."}</p>}
          <p className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-fg-subtle">
            <StateBadge state={run.status} />
            <span>{formatNumber(run.fresh_tokens)} tokens</span>
            <Link to={`/runs/${run.id}`} className="focus-ring rounded underline">
              détail
            </Link>
          </p>
        </>
      )}
    </Bubble>
  );
}

function ApprovalItem({ a }: { a: Approval }) {
  const pending = a.status === "pending";
  return (
    <li className="flex justify-center" data-testid={`thread-approval-${a.id}`}>
      <Link
        to={`/approvals/${a.id}`}
        className={cn(
          "focus-ring max-w-[90%] rounded-md border px-3 py-1.5 text-xs",
          pending ? "border-review/50 bg-review/10 text-review" : "border-border bg-panel text-fg-muted",
        )}
      >
        <b>{pending ? "À décider" : "Décision"}</b> · {APPROVAL_KIND_LABELS[a.kind] ?? a.kind} : {a.title}
        {!pending && a.decided_by && (
          <>
            {" "}
            — {a.decision === "approved" ? "approuvée" : a.decision === "rejected" ? "refusée" : a.status} par {a.decided_by}
          </>
        )}
      </Link>
    </li>
  );
}

const STATUS_TEXT: Record<string, string> = {
  delivered: "transmise à l'agent en cours",
  pending: "gardée pour son prochain passage",
  consumed: "ajoutée aux instructions de l'agent",
  answering: "l'agent prépare sa réponse…",
  answered: "répondue",
  failed: "sans réponse",
};

function MessageItem({
  m,
  runs,
  onCorrect,
  question,
}: {
  m: FeatureMessage;
  runs: Map<number, ThreadRun>;
  onCorrect?: (d: CorrectionDraft) => void;
  question?: FeatureMessage;
}) {
  if (m.kind === "answer") {
    const run = m.run_id ? runs.get(m.run_id) : undefined;
    return (
      <Bubble
        testid={`thread-answer-${m.id}`}
        avatar={<Avatar role={m.target_role} />}
        head={
          <>
            <b className="text-fg">{roleLabel(m.target_role)}</b>
            <span>répond</span>
            <Time value={m.created_at} />
          </>
        }
      >
        <p className="whitespace-pre-wrap">{m.body}</p>
        <p className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-fg-subtle">
          {run && <span>{formatNumber(run.fresh_tokens)} tokens</span>}
          {m.run_id && (
            <Link to={`/runs/${m.run_id}`} className="focus-ring rounded underline">
              détail
            </Link>
          )}
          {onCorrect && (
            <Button
              size="sm"
              variant="secondary"
              icon={<MessageSquareWarning className="h-3.5 w-3.5" aria-hidden />}
              onClick={() =>
                onCorrect({
                  title: question ? question.body.slice(0, 100) : "Correction",
                  description:
                    (question ? `Question : ${question.body}\n\n` : "") + `Réponse du ${roleLabel(m.target_role).toLowerCase()} :\n${m.body}`.slice(0, 3800),
                })
              }
            >
              Transformer en correction
            </Button>
          )}
        </p>
      </Bubble>
    );
  }
  const who = login(m.author);
  const label = m.kind === "question" ? "Question" : "Consigne";
  return (
    <Bubble
      mine
      testid={`thread-${m.kind}-${m.id}`}
      avatar={<Avatar me={who} />}
      head={
        <>
          <b className="text-fg">{who}</b>
          <span>
            {label} → {m.target_role ? roleLabel(m.target_role) : "agent en cours"}
          </span>
          <Time value={m.created_at} />
        </>
      }
    >
      <p className="whitespace-pre-wrap">{m.body}</p>
      <p className={cn("mt-1 text-xs", m.status === "failed" ? "text-danger" : "text-fg-subtle")} data-testid="message-status">
        {m.status === "answering" && <span className="mr-1 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-progress align-middle" aria-hidden />}
        {STATUS_TEXT[m.status] ?? m.status}
        {m.note && <> · {m.note}</>}
      </p>
    </Bubble>
  );
}

function Composer({ t, featureId }: { t: Thread; featureId: number }) {
  const m = usePostMessage(featureId);
  const toast = useToast();
  const [kind, setKind] = useState<Kind>("instruction");
  const [role, setRole] = useState<string>("");
  const [body, setBody] = useState("");
  const questionRole = role || t.working || "po";
  const busy = kind === "question" && t.working === questionRole;
  const tooLong = body.length > MAX_MESSAGE;
  const send = (e?: FormEvent) => {
    e?.preventDefault();
    if (!body.trim() || tooLong || busy) return;
    m.mutate(
      { kind, role: kind === "question" ? questionRole : role, body: body.trim() },
      {
        onSuccess: (msg) => {
          setBody("");
          if (msg?.status === "delivered") toast.success("Consigne transmise", "L'agent en cours la prend en compte à son prochain pas.");
          else if (msg?.kind === "question") toast.success("Question envoyée", `${roleLabel(questionRole)} répond dans le fil.`);
          else toast.success("Consigne enregistrée", "Elle sera donnée à l'agent à son prochain passage.");
        },
        onError: (err) => toast.error("Message refusé", errorMessage(err)),
      },
    );
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) send();
  };
  if (t.closed) return <p className="border-t border-border px-4 py-3 text-sm text-fg-subtle">Feature terminée : la discussion est close.</p>;
  const hint =
    kind === "instruction"
      ? t.working
        ? `Consigne : transmise tout de suite à l'agent en cours (${roleLabel(t.working).toLowerCase()}), sans le redémarrer. Tracée dans l'audit.`
        : "Consigne : aucun agent ne travaille en ce moment, elle sera ajoutée aux instructions de son prochain passage. Tracée dans l'audit."
      : busy
        ? "Cet agent travaille en ce moment : envoie-lui plutôt une consigne."
        : `Question : ${roleLabel(questionRole).toLowerCase()} répond ${t.sessions[questionRole] ? "dans sa session sur la feature (tout son contexte)" : "avec le contexte de la feature"}, sans rien modifier. Quelques milliers de tokens. Tracée dans l'audit.`;
  return (
    <form onSubmit={send} className="space-y-2 border-t border-border px-4 py-3" aria-label="Écrire aux agents" data-testid="composer">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <div className="inline-flex rounded-md border border-border p-0.5" role="group" aria-label="Type de message">
          {(["instruction", "question"] as const).map((k) => (
            <button
              key={k}
              type="button"
              aria-pressed={kind === k}
              onClick={() => setKind(k)}
              className={cn("focus-ring rounded px-2.5 py-1 text-xs font-medium", kind === k ? "bg-accent text-accent-fg" : "text-fg-muted hover:text-fg")}
            >
              {k === "instruction" ? "Consigne" : "Question"}
            </button>
          ))}
        </div>
        <label className="flex items-center gap-1.5 text-xs text-fg-muted">
          À
          <Select
            aria-label="Destinataire"
            value={kind === "question" ? questionRole : role}
            onChange={(e) => setRole(e.target.value)}
            className="h-8 w-auto py-0 text-xs"
          >
            {kind === "instruction" && <option value="">{t.working ? `Agent en cours (${roleLabel(t.working)})` : "Prochain agent"}</option>}
            {CHAT_ROLES.map((r) => (
              <option key={r} value={r}>
                {roleLabel(r)}
              </option>
            ))}
          </Select>
        </label>
      </div>
      <div className="flex items-end gap-2">
        <Textarea
          aria-label="Message"
          rows={2}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={onKey}
          placeholder={kind === "instruction" ? "Pense au cas où la configuration est incomplète." : "Pourquoi ce choix ?"}
          className="min-h-[60px] flex-1"
        />
        <Button
          type="submit"
          variant="primary"
          loading={m.isPending}
          disabled={!body.trim() || tooLong || busy}
          icon={<Send className="h-3.5 w-3.5" aria-hidden />}
        >
          Envoyer
        </Button>
      </div>
      <p className={cn("text-xs", busy || tooLong ? "text-danger" : "text-fg-subtle")} data-testid="composer-hint">
        {tooLong ? `${MAX_MESSAGE} caractères maximum.` : hint} <span className="hidden xl:inline">Ctrl+Entrée pour envoyer.</span>
      </p>
    </form>
  );
}

/** Fil d'équipe d'une feature : passages des agents, décisions, consignes et questions. */
export function FeatureThread({ featureId, onCorrect }: { featureId: number; onCorrect?: (d: CorrectionDraft) => void }) {
  const q = useFeatureThread(featureId);
  const listRef = useRef<HTMLOListElement>(null);
  const items = useMemo(() => (q.data ? threadItems(q.data) : []), [q.data]);
  const runs = useMemo(() => new Map((q.data?.runs ?? []).map((r) => [r.id, r])), [q.data]);
  const questions = useMemo(() => new Map((q.data?.messages ?? []).filter((m) => m.kind === "question").map((m) => [m.id, m])), [q.data]);
  // Nouveau message ou passage : le fil défile jusqu'en bas.
  useEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [items.length]);
  if (q.isPending) return <Skeleton className="h-64" />;
  if (!q.data) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const t = q.data;
  return (
    <div className="-mx-4 -mb-4" data-testid="feature-thread">
      {items.length === 0 ? (
        <div className="px-4 pb-2">
          <EmptyState title="Rien encore" description="Les passages des agents, tes décisions et vos échanges apparaîtront ici." />
        </div>
      ) : (
        <ol ref={listRef} className="scroll-thin max-h-[60vh] space-y-3 overflow-y-auto px-4 pb-3" aria-live="polite" aria-label="Fil d'équipe" tabIndex={0}>
          {items.map((it) =>
            it.type === "run" ? (
              <RunItem key={`r${it.run.id}`} run={it.run} />
            ) : it.type === "approval" ? (
              <ApprovalItem key={`a${it.approval.id}`} a={it.approval} />
            ) : (
              <MessageItem
                key={`m${it.message.id}`}
                m={it.message}
                runs={runs}
                onCorrect={onCorrect}
                question={it.message.reply_to ? questions.get(it.message.reply_to) : undefined}
              />
            ),
          )}
        </ol>
      )}
      <Composer t={t} featureId={featureId} />
    </div>
  );
}
