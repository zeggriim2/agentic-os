// Pages de décision structurées (1.13) : revue de spec, revue de conception, reprise après revue.
// Questions des agents en cartes (réponse en un clic + champ libre toujours disponible),
// « Demander à l'agent » via le fil d'équipe, brouillon enregistré localement au fil de l'eau.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, HelpCircle, Loader2, MessageSquare, Send, Sparkles, X } from "lucide-react";
import { ApiError, errorMessage } from "@/api/client";
import { useDecideApproval, useFeatureThread, usePostMessage, useTicket } from "@/api/queries";
import type { Approval } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Card, CardHeader, Textarea } from "@/components/ui/primitives";
import { useToast } from "@/components/ui/Toast";
import { cn } from "@/lib/cn";
import {
  askBody,
  clearDraft,
  composeFeedback,
  composeRework,
  countAnswered,
  emptyAnswer,
  findingItems,
  isAnswered,
  loadDraft,
  parseQuestions,
  saveDraft,
  type Answer,
  type Draft,
  type NoteRef,
  type OpenQuestion,
} from "@/lib/decision";

type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj | null => (v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : null);
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
const str = (v: unknown): string => (typeof v === "string" ? v : "");

/** Vrai si la page structurée gère ce type de décision. */
export function hasStructuredReview(a: Approval): boolean {
  return a.status === "pending" && (a.kind === "spec_review" || a.kind === "design_review" || a.kind === "rework");
}

/** Brouillon persistant de l'approbation, enregistré à chaque modification. */
function useDraft(id: number) {
  const [draft, setDraft] = useState<Draft>(() => loadDraft(id));
  const [savedAt, setSavedAt] = useState(0);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    saveDraft(id, draft);
    setSavedAt(Date.now());
  }, [id, draft]);
  return { draft, setDraft, savedAt };
}

function useFeatureId(a: Approval): number {
  const ticket = useTicket(a.subject_type === "ticket" ? a.subject_id : 0);
  if (a.subject_type === "feature") return a.subject_id;
  return a.subject_type === "ticket" ? (ticket.data?.feature_id ?? 0) : 0;
}

// ---------------------------------------------------------------------------------------------
// Carte de question

function QuestionCard({
  index,
  q,
  answer,
  onChange,
  agent,
  role,
  featureId,
}: {
  index: number;
  q: OpenQuestion;
  answer: Answer;
  onChange: (a: Answer) => void;
  agent: string;
  role: string;
  featureId: number;
}) {
  const done = isAnswered(answer);
  const status = answer.delegate ? `${agent} décide` : done ? "répondu" : "à traiter";
  return (
    <article
      aria-label={`Question ${index + 1}`}
      data-testid="question-card"
      className={cn(
        "rounded-lg border border-border border-l-[3px] bg-panel p-3.5",
        answer.delegate ? "border-l-accent" : done ? "border-l-success" : "border-l-review",
      )}
    >
      <div className="flex items-start gap-2.5">
        <span
          className={cn(
            "grid h-6 w-6 flex-none place-items-center rounded-full text-xs font-bold",
            done ? "bg-success/15 text-success" : "bg-review/15 text-review",
          )}
          aria-hidden
        >
          {index + 1}
        </span>
        <p className="flex-1 text-sm text-fg">{q.question}</p>
        <span className={cn("whitespace-nowrap text-xs", done ? "text-success" : "text-fg-subtle")}>{status}</span>
      </div>
      <div className="ml-8 mt-2.5 space-y-2">
        <div className="flex flex-wrap gap-1.5" role="group" aria-label={`Réponses proposées à la question ${index + 1}`}>
          {q.options.map((o, k) => (
            <button
              key={k}
              type="button"
              aria-pressed={answer.choice === k}
              onClick={() =>
                onChange({
                  ...answer,
                  choice: answer.choice === k ? null : k,
                  delegate: false,
                })
              }
              className={cn(
                "rounded-md border px-2.5 py-1 text-[13px] transition-colors",
                answer.choice === k ? "border-review bg-review font-semibold text-bg" : "border-border bg-surface text-fg hover:border-review/60",
              )}
            >
              {o}
              {q.recommended === k && (
                <span className={cn("ml-1.5 text-2xs", answer.choice === k ? "text-bg/80" : "text-accent")}>
                  <Sparkles className="mr-0.5 inline h-3 w-3" aria-hidden />
                  recommandé
                </span>
              )}
            </button>
          ))}
          <button
            type="button"
            aria-pressed={answer.delegate}
            onClick={() =>
              onChange({
                ...answer,
                delegate: !answer.delegate,
                choice: answer.delegate ? answer.choice : null,
              })
            }
            className={cn(
              "rounded-md border px-2.5 py-1 text-[13px] transition-colors",
              answer.delegate ? "border-accent bg-accent-solid font-semibold text-accent-fg" : "border-border bg-surface text-fg-muted hover:border-accent/60",
            )}
          >
            Laisser {agent === "PO" ? "le PO" : `le ${agent.toLowerCase()}`} décider
          </button>
        </div>
        <Textarea
          rows={1}
          className="min-h-[38px]"
          value={answer.text}
          aria-label={`Ta réponse à la question ${index + 1}`}
          placeholder={q.options.length ? "Ta propre réponse ou une précision (facultatif)" : "Ta réponse"}
          onChange={(e) => onChange({ ...answer, text: e.target.value, delegate: false })}
        />
        <AskAgent index={index} q={q} answer={answer} onChange={onChange} agent={agent} role={role} featureId={featureId} />
      </div>
    </article>
  );
}

function AskAgent({
  index,
  q,
  answer,
  onChange,
  agent,
  role,
  featureId,
}: {
  index: number;
  q: OpenQuestion;
  answer: Answer;
  onChange: (a: Answer) => void;
  agent: string;
  role: string;
  featureId: number;
}) {
  const post = usePostMessage(featureId);
  const thread = useFeatureThread(featureId, answer.askedId !== undefined);
  const toast = useToast();
  const msgs = thread.data?.messages ?? [];
  const asked = answer.askedId !== undefined ? msgs.find((m) => m.id === answer.askedId) : undefined;
  const reply = answer.askedId !== undefined ? msgs.find((m) => m.reply_to === answer.askedId && m.kind === "answer") : undefined;
  const failed = asked?.status === "failed";
  const ask = () =>
    post.mutate(
      { kind: "question", role, body: askBody(index, q, answer.text) },
      {
        onSuccess: (m) => onChange({ ...answer, askedId: m.id }),
        onError: (e) => toast.error(`Question non envoyée au ${agent}`, errorMessage(e)),
      },
    );
  if (!featureId) return null;
  return (
    <div className="text-[12.5px]">
      {answer.askedId === undefined || failed ? (
        <button
          type="button"
          onClick={ask}
          disabled={post.isPending}
          className="inline-flex items-center gap-1 text-accent underline underline-offset-2 disabled:opacity-50"
        >
          {post.isPending ? <Loader2 className="h-3 w-3 animate-spin" aria-hidden /> : <HelpCircle className="h-3 w-3" aria-hidden />}
          {failed ? `Réessayer : demander au ${agent}` : `Demander au ${agent}`}
        </button>
      ) : (
        <div className="rounded-md bg-accent/10 px-2.5 py-2" role="status" data-testid="agent-reply">
          <span className="font-semibold text-accent">{agent}</span>{" "}
          {reply ? (
            <>
              — <span className="whitespace-pre-wrap text-fg">{reply.body}</span>
              {q.recommended !== null && answer.choice !== q.recommended && (
                <div className="mt-1.5">
                  <button
                    type="button"
                    className="text-accent underline underline-offset-2"
                    onClick={() =>
                      onChange({
                        ...answer,
                        choice: q.recommended,
                        delegate: false,
                      })
                    }
                  >
                    Reprendre sa recommandation
                  </button>
                </div>
              )}
            </>
          ) : (
            <span className="text-fg-muted">
              <Loader2 className="mr-1 inline h-3 w-3 animate-spin" aria-hidden />
              répond… (il reprend sa session sur la feature ; la réponse arrive aussi dans le fil d'équipe)
            </span>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Éléments communs

function Progress({ done, total, savedAt }: { done: number; total: number; savedAt: number }) {
  const [show, setShow] = useState(false);
  useEffect(() => {
    if (!savedAt) return;
    setShow(true);
    const t = setTimeout(() => setShow(false), 1400);
    return () => clearTimeout(t);
  }, [savedAt]);
  return (
    <div className="flex items-center gap-2 text-sm">
      <span className={cn("text-xs text-success transition-opacity", show ? "opacity-100" : "opacity-0")} aria-hidden={!show}>
        ✓ brouillon enregistré
      </span>
      <span data-testid="answered-count">
        {done} / {total} traitée{total > 1 ? "s" : ""}
      </span>
      <div className="h-1.5 w-36 overflow-hidden rounded bg-surface" aria-hidden>
        <div className="h-full bg-review transition-[width]" style={{ width: total ? `${(done / total) * 100}%` : "0%" }} />
      </div>
    </div>
  );
}

function NoteButton({ k, label, draft, setDraft }: { k: string; label: string; draft: Draft; setDraft: (f: (d: Draft) => Draft) => void }) {
  const [open, setOpen] = useState(false);
  const value = draft.notes[k] ?? "";
  const set = (v: string) =>
    setDraft((d) => {
      const notes = { ...d.notes };
      if (v.trim()) notes[k] = v;
      else delete notes[k];
      return { ...d, notes };
    });
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-label={`Commenter : ${label}`}
        aria-expanded={open}
        className={cn("flex-none rounded p-0.5", value ? "text-review" : "text-fg-subtle hover:text-review")}
      >
        <MessageSquare className="h-3.5 w-3.5" aria-hidden />
      </button>
      {(open || value) && (
        <div className="basis-full pl-6 pt-1.5">
          {open ? (
            <Textarea
              rows={2}
              autoFocus
              value={value}
              aria-label={`Ta remarque : ${label}`}
              placeholder="Ta remarque"
              onChange={(e) => set(e.target.value)}
              onBlur={() => setOpen(false)}
            />
          ) : (
            <p className="rounded-md bg-review/10 px-2 py-1 text-xs text-fg">Ta remarque : {value}</p>
          )}
        </div>
      )}
    </>
  );
}

function Section({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-1.5">
      <h3 className="label-caps text-fg-subtle">{title}</h3>
      {children}
    </section>
  );
}

function DecisionBar({ summary, children }: { summary: ReactNode; children: ReactNode }) {
  return (
    <div
      className="sticky bottom-0 z-10 -mx-1 mt-4 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-panel/95 px-4 py-3 backdrop-blur"
      data-testid="decision-bar"
    >
      <span className="mr-auto text-sm text-fg-muted">{summary}</span>
      {children}
    </div>
  );
}

function useDecide(a: Approval) {
  const m = useDecideApproval(a.id);
  const toast = useToast();
  const decide = (decision: "approved" | "rejected", comment: string, ok: string) =>
    m.mutate(
      { decision, comment },
      {
        onSuccess: () => {
          clearDraft(a.id);
          toast.success(ok);
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409) toast.info("Déjà décidée", "L'état à jour a été rechargé.");
          else toast.error("Décision non enregistrée", errorMessage(e));
        },
      },
    );
  return { m, decide };
}

function summarize(done: number, delegated: number, total: number, notes: number): string {
  const parts: string[] = [];
  const answered = done - delegated;
  if (answered) parts.push(`${answered} réponse${answered > 1 ? "s" : ""}`);
  if (delegated) parts.push(`${delegated} laissée${delegated > 1 ? "s" : ""} à l'agent`);
  if (notes) parts.push(`${notes} remarque${notes > 1 ? "s" : ""}`);
  if (total - done > 0) parts.push(`${total - done} sans réponse`);
  return parts.length ? parts.join(" · ") : "Aucune réponse pour l'instant.";
}

function QuestionList({
  qs,
  draft,
  setDraft,
  agent,
  role,
  featureId,
}: {
  qs: OpenQuestion[];
  draft: Draft;
  setDraft: (f: (d: Draft) => Draft) => void;
  agent: string;
  role: string;
  featureId: number;
}) {
  const [filter, setFilter] = useState<"all" | "todo" | "done">("all");
  const shown = qs.map((q, i) => ({ q, i })).filter(({ i }) => filter === "all" || (filter === "todo") !== isAnswered(draft.answers[i]));
  return (
    <div className="space-y-2.5">
      <div className="flex gap-1.5" role="group" aria-label="Filtrer les questions">
        {(
          [
            ["all", "Toutes"],
            ["todo", "À traiter"],
            ["done", "Traitées"],
          ] as const
        ).map(([k, l]) => (
          <button
            key={k}
            type="button"
            aria-pressed={filter === k}
            onClick={() => setFilter(k)}
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-xs",
              filter === k ? "border-review/50 bg-review/10 text-review" : "border-border bg-surface text-fg-muted",
            )}
          >
            {l}
          </button>
        ))}
      </div>
      {shown.map(({ q, i }) => (
        <QuestionCard
          key={i}
          index={i}
          q={q}
          agent={agent}
          role={role}
          featureId={featureId}
          answer={draft.answers[i] ?? emptyAnswer()}
          onChange={(ans) => setDraft((d) => ({ ...d, answers: { ...d.answers, [i]: ans } }))}
        />
      ))}
      {shown.length === 0 && <p className="text-sm text-fg-subtle">Rien ici.</p>}
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Revue de spec

export function SpecReview({ a }: { a: Approval }) {
  const details = a.details ?? {};
  const spec = obj(details.spec) ?? {};
  const qs = useMemo(() => parseQuestions(details.open_questions ?? spec.open_questions), [details.open_questions, spec.open_questions]);
  const stories = strs(spec.user_stories);
  const criteria = strs(spec.acceptance_criteria);
  const { draft, setDraft, savedAt } = useDraft(a.id);
  const featureId = useFeatureId(a);
  const { m, decide } = useDecide(a);
  const [confirm, setConfirm] = useState(false);
  const noteRefs: NoteRef[] = criteria.map((c, i) => ({
    key: `c${i}`,
    label: `critère ${i + 1} (« ${c.slice(0, 90)}${c.length > 90 ? "…" : ""} »)`,
  }));
  const done = countAnswered(qs, draft);
  const delegated = qs.filter((_q, i) => draft.answers[i]?.delegate).length;
  const nNotes = Object.keys(draft.notes).length;
  const toPO = composeFeedback(qs, draft, "PO", noteRefs);
  const canSend = done > 0 || nNotes > 0 || draft.remark.trim() !== "";
  const approve = () => decide("approved", canSend ? composeFeedback(qs, draft, "tech lead", noteRefs) : "", "Spec approuvée : la conception démarre.");

  return (
    <div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-sm font-semibold text-fg">
              Questions du PO <span className="font-normal text-fg-muted">— un clic sur une réponse proposée, ou écris la tienne</span>
            </h2>
            {qs.length > 0 && (
              <div className="ml-auto">
                <Progress done={done} total={qs.length} savedAt={savedAt} />
              </div>
            )}
          </div>
          {qs.length ? (
            <QuestionList qs={qs} draft={draft} setDraft={setDraft} agent="PO" role="po" featureId={featureId} />
          ) : (
            <p className="rounded-lg border border-border bg-panel p-3 text-sm text-fg-muted">Le PO n'a aucune question ouverte.</p>
          )}
          <Card>
            <div className="space-y-1.5 p-3">
              <label htmlFor="spec-remark" className="text-xs font-medium text-fg-muted">
                Autre remarque pour le PO (facultatif)
              </label>
              <Textarea
                id="spec-remark"
                data-testid="decision-comment"
                rows={2}
                value={draft.remark}
                onChange={(e) => setDraft((d) => ({ ...d, remark: e.target.value }))}
              />
            </div>
          </Card>
        </div>
        <Card className="self-start">
          <CardHeader title="La spécification" description="Survole un critère et clique sur la bulle pour le commenter." />
          <div className="space-y-4 p-4 text-sm">
            {str(spec.summary) && (
              <Section title="Résumé">
                <p className="whitespace-pre-wrap text-fg">{str(spec.summary)}</p>
              </Section>
            )}
            {stories.length > 0 && (
              <Section title={`User stories (${stories.length})`}>
                <details>
                  <summary className="cursor-pointer text-fg-muted">Afficher les {stories.length} user stories</summary>
                  <ul className="mt-1 divide-y divide-border">
                    {stories.map((s, i) => (
                      <li key={i} className="py-1.5 text-[13px]">
                        {s}
                      </li>
                    ))}
                  </ul>
                </details>
              </Section>
            )}
            {criteria.length > 0 && (
              <Section title={`Critères d'acceptation (${criteria.length})`}>
                <ul className="divide-y divide-border" aria-label="Critères d'acceptation">
                  {criteria.map((c, i) => (
                    <li key={i} className="group flex flex-wrap items-start gap-2 py-1.5 text-[13px]">
                      <span className="w-5 flex-none text-xs text-fg-subtle">{i + 1}.</span>
                      <span className="flex-1">{c}</span>
                      <NoteButton k={`c${i}`} label={`critère ${i + 1}`} draft={draft} setDraft={setDraft} />
                    </li>
                  ))}
                </ul>
              </Section>
            )}
          </div>
        </Card>
      </div>
      <DecisionBar summary={summarize(done, delegated, qs.length, nNotes)}>
        <Button
          onClick={() => decide("rejected", toPO, "Réponses envoyées au PO : il révise la spec.")}
          disabled={!canSend || m.isPending}
          loading={m.isPending && m.variables?.decision === "rejected"}
          icon={<Send className="h-4 w-4" aria-hidden />}
          className="border-review/40 bg-review/10 text-review hover:bg-review/20"
        >
          Envoyer mes réponses au PO
        </Button>
        <Button
          variant="primary"
          data-testid="approve-button"
          onClick={() => (qs.length - done > 0 ? setConfirm(true) : approve())}
          disabled={m.isPending}
          loading={m.isPending && m.variables?.decision === "approved"}
          icon={<Check className="h-4 w-4" aria-hidden />}
        >
          Approuver et lancer la conception
        </Button>
      </DecisionBar>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false);
          approve();
        }}
        title="Approuver avec des questions sans réponse ?"
        description={`${qs.length - done} question${qs.length - done > 1 ? "s restent" : " reste"} sans réponse. Si tu approuves, la spec part telle quelle au tech lead, qui tranchera ces points lui-même. Tes réponses sont transmises au tech lead.`}
        confirmLabel="Approuver quand même"
      />
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Revue de conception

export function DesignReview({ a }: { a: Approval }) {
  const details = a.details ?? {};
  const design = obj(details.design) ?? {};
  const qs = useMemo(() => parseQuestions(design.open_questions ?? details.open_questions), [design.open_questions, details.open_questions]);
  const tickets = (Array.isArray(design.tickets) ? design.tickets : []).map(obj).filter((t): t is Obj => !!t);
  const titles = tickets.length ? tickets.map((t) => str(t.title)) : strs(details.tickets);
  const decisions = (Array.isArray(design.decisions) ? design.decisions : []).map(obj).filter((d): d is Obj => !!d);
  const risks = strs(design.risks);
  const { draft, setDraft, savedAt } = useDraft(a.id);
  const featureId = useFeatureId(a);
  const { m, decide } = useDecide(a);
  const [confirm, setConfirm] = useState(false);
  const noteRefs: NoteRef[] = [
    ...titles.map((t, i) => ({
      key: `t${i}`,
      label: `ticket T${i + 1} (« ${t} »)`,
    })),
    ...decisions.map((d, i) => ({
      key: `d${i}`,
      label: `décision « ${str(d.decision)} »`,
    })),
  ];
  const done = countAnswered(qs, draft);
  const delegated = qs.filter((_q, i) => draft.answers[i]?.delegate).length;
  const nNotes = Object.keys(draft.notes).length;
  const feedback = composeFeedback(qs, draft, "tech lead", noteRefs);
  const hasInput = done - delegated > 0 || nNotes > 0 || draft.remark.trim() !== "";
  const approve = () => decide("approved", feedback, `Conception approuvée : création de ${titles.length} issue${titles.length > 1 ? "s" : ""}.`);

  return (
    <div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="space-y-3">
          <h2 className="text-sm font-semibold text-fg">
            Tickets proposés <span className="font-normal text-fg-muted">— ce qui sera développé, relu, puis mergé par toi</span>
          </h2>
          {(tickets.length ? tickets : titles.map((t) => ({ title: t }) as Obj)).map((t, i) => {
            const crit = strs(t.acceptance_criteria);
            return (
              <div key={i} className="rounded-lg border border-border bg-panel p-3" data-testid="design-ticket">
                <div className="flex flex-wrap items-start gap-2">
                  <span className="flex-1 font-semibold text-fg">
                    T{i + 1} · {str(t.title)}
                  </span>
                  <NoteButton k={`t${i}`} label={`ticket T${i + 1}`} draft={draft} setDraft={setDraft} />
                </div>
                {str(t.body) && <p className="mt-1 line-clamp-3 whitespace-pre-wrap text-[13px] text-fg-muted">{str(t.body)}</p>}
                {crit.length > 0 && (
                  <details className="mt-1 text-[13px]">
                    <summary className="cursor-pointer text-fg-subtle">
                      {crit.length} critère{crit.length > 1 ? "s" : ""}
                    </summary>
                    <ul className="mt-1 list-disc pl-5">
                      {crit.map((c, j) => (
                        <li key={j}>{c}</li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
            );
          })}
          {qs.length > 0 && (
            <>
              <div className="flex flex-wrap items-center gap-3 pt-2">
                <h2 className="text-sm font-semibold text-fg">Questions du tech lead</h2>
                <div className="ml-auto">
                  <Progress done={done} total={qs.length} savedAt={savedAt} />
                </div>
              </div>
              <QuestionList qs={qs} draft={draft} setDraft={setDraft} agent="Tech lead" role="tech_lead" featureId={featureId} />
            </>
          )}
          <Card>
            <div className="space-y-1.5 p-3">
              <label htmlFor="design-remark" className="text-xs font-medium text-fg-muted">
                Autre remarque pour le tech lead (facultatif)
              </label>
              <Textarea
                id="design-remark"
                data-testid="decision-comment"
                rows={2}
                value={draft.remark}
                onChange={(e) => setDraft((d) => ({ ...d, remark: e.target.value }))}
              />
            </div>
          </Card>
        </div>
        <Card className="self-start">
          <CardHeader title="La conception" />
          <div className="space-y-4 p-4 text-sm">
            {str(design.summary) && (
              <Section title="Résumé">
                <p className="whitespace-pre-wrap text-fg">{str(design.summary)}</p>
              </Section>
            )}
            {decisions.length > 0 && (
              <Section title={`Décisions (${decisions.length})`}>
                <ul className="space-y-2">
                  {decisions.map((d, i) => (
                    <li key={i} className="flex flex-wrap items-start gap-2 rounded-md border border-border bg-surface p-2.5">
                      <div className="flex-1">
                        <p className="font-medium text-fg">{str(d.decision)}</p>
                        {str(d.rationale) && <p className="text-[13px] text-fg-muted">{str(d.rationale)}</p>}
                        {strs(d.rejected_alternatives).length > 0 && (
                          <p className="mt-0.5 text-xs text-fg-subtle">
                            Écartée
                            {strs(d.rejected_alternatives).length > 1 ? "s" : ""} : {strs(d.rejected_alternatives).join(" ; ")}
                          </p>
                        )}
                      </div>
                      <NoteButton k={`d${i}`} label={`décision ${i + 1}`} draft={draft} setDraft={setDraft} />
                    </li>
                  ))}
                </ul>
              </Section>
            )}
            {risks.length > 0 && (
              <Section title={`Risques (${risks.length})`}>
                <ul className="list-disc space-y-1 pl-5 text-[13px]">
                  {risks.map((r, i) => (
                    <li key={i}>{r}</li>
                  ))}
                </ul>
              </Section>
            )}
          </div>
        </Card>
      </div>
      <DecisionBar
        summary={
          qs.length || nNotes ? summarize(done, delegated, qs.length, nNotes) : "Ce qui part au tech lead : tes commentaires sur les tickets et les décisions."
        }
      >
        <Button
          onClick={() => decide("rejected", feedback, "Conception renvoyée au tech lead avec tes remarques.")}
          disabled={!feedback || m.isPending}
          loading={m.isPending && m.variables?.decision === "rejected"}
          icon={<Send className="h-4 w-4" aria-hidden />}
          className="border-review/40 bg-review/10 text-review hover:bg-review/20"
        >
          Renvoyer au tech lead avec mes remarques
        </Button>
        <Button
          variant="primary"
          data-testid="approve-button"
          onClick={() => (hasInput || qs.length - done > 0 ? setConfirm(true) : approve())}
          disabled={m.isPending}
          loading={m.isPending && m.variables?.decision === "approved"}
          icon={<Check className="h-4 w-4" aria-hidden />}
        >
          Approuver et créer {titles.length} issue{titles.length > 1 ? "s" : ""}
        </Button>
      </DecisionBar>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => {
          setConfirm(false);
          approve();
        }}
        title="Créer les issues telles quelles ?"
        description={
          hasInput
            ? "Approuver crée les issues de la conception actuelle : tes réponses et remarques sont seulement enregistrées, elles ne modifient pas les tickets. Pour qu'elles soient intégrées, renvoie la conception au tech lead."
            : "Des questions du tech lead restent sans réponse : les issues seront créées avec la conception actuelle."
        }
        confirmLabel="Créer les issues quand même"
      />
    </div>
  );
}

// ---------------------------------------------------------------------------------------------
// Reprise après revue / QA

export function ReworkReview({ a }: { a: Approval }) {
  const details = a.details ?? {};
  const items = useMemo(() => findingItems(details), [details]);
  const source = str(details.source);
  const role = source === "qa" ? "qa" : "reviewer";
  const agent = source === "qa" ? "QA" : "Reviewer";
  const attempt = typeof details.attempt === "number" ? details.attempt : 0;
  const max = typeof details.max_rework === "number" ? details.max_rework : 0;
  const exhausted = max > 0 && attempt >= max;
  const { draft, setDraft } = useDraft(a.id);
  const featureId = useFeatureId(a);
  const { m, decide } = useDecide(a);
  const [abandon, setAbandon] = useState(false);
  const [asked, setAsked] = useState<Record<number, Answer>>({});
  const kept = items.length - draft.dropped.filter((i) => i < items.length).length;
  const comment = composeRework(items, draft);
  const toggle = (i: number, keep: boolean) =>
    setDraft((d) => ({
      ...d,
      dropped: keep ? d.dropped.filter((x) => x !== i) : [...new Set([...d.dropped, i])],
    }));

  return (
    <div>
      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="space-y-3">
          <h2 className="text-sm font-semibold text-fg">
            Constats {source === "qa" ? "du QA" : "du reviewer"}{" "}
            <span className="font-normal text-fg-muted">— le développeur ne traite que ceux que tu gardes</span>
          </h2>
          {items.map((f, i) => {
            const keep = !draft.dropped.includes(i);
            const sev = /^- \[(P[0-3])\]/.exec(f)?.[1];
            return (
              <div key={i} className={cn("rounded-lg border border-border bg-panel p-3", !keep && "opacity-60")} data-testid="finding">
                <p className="text-sm text-fg">
                  {sev && (
                    <span
                      className={cn(
                        "mr-1.5 rounded px-1.5 text-2xs font-bold",
                        sev === "P0" || sev === "P1" ? "bg-danger/15 text-danger" : "bg-warning/15 text-warning",
                      )}
                    >
                      {sev}
                    </span>
                  )}
                  <span className={cn(!keep && "line-through")}>{f.replace(/^- (\[P[0-3]\] )?/, "")}</span>
                </p>
                <div className="mt-2 flex flex-wrap items-center gap-1.5" role="group" aria-label={`Constat ${i + 1}`}>
                  {(
                    [
                      [true, "D'accord"],
                      [false, "Pas d'accord"],
                    ] as const
                  ).map(([v, l]) => (
                    <button
                      key={l}
                      type="button"
                      aria-pressed={keep === v}
                      onClick={() => toggle(i, v)}
                      className={cn(
                        "rounded-md border px-2.5 py-0.5 text-[13px]",
                        keep === v ? "border-review bg-review font-semibold text-bg" : "border-border bg-surface text-fg",
                      )}
                    >
                      {l}
                    </button>
                  ))}
                  <span className="ml-2">
                    <AskAgent
                      index={i}
                      q={{
                        question: `Constat : ${f.replace(/^- /, "")}`,
                        options: [],
                        recommended: null,
                      }}
                      answer={
                        asked[i] ?? {
                          ...emptyAnswer(),
                          text: "Est-ce bloquant ? Que se passe-t-il si on ne le traite pas ?",
                        }
                      }
                      onChange={(ans) => setAsked((s) => ({ ...s, [i]: ans }))}
                      agent={agent}
                      role={role}
                      featureId={featureId}
                    />
                  </span>
                </div>
              </div>
            );
          })}
          {items.length === 0 && <p className="text-sm text-fg-muted">Aucun constat détaillé.</p>}
          <Card>
            <div className="space-y-1.5 p-3">
              <label htmlFor="rework-remark" className="text-xs font-medium text-fg-muted">
                Ta remarque au développeur (facultatif)
              </label>
              <Textarea
                id="rework-remark"
                data-testid="decision-comment"
                rows={2}
                value={draft.remark}
                onChange={(e) => setDraft((d) => ({ ...d, remark: e.target.value }))}
              />
            </div>
          </Card>
        </div>
        <Card className="self-start">
          <CardHeader title="Contexte" />
          <div className="space-y-3 p-4 text-sm">
            {str(details.summary) && (
              <Section title={`Synthèse ${source === "qa" ? "du QA" : source === "review" ? "du reviewer" : ""}`}>
                <p className="whitespace-pre-wrap text-fg">{str(details.summary)}</p>
              </Section>
            )}
            <Section title="Tentative">
              <p className="text-fg">
                {attempt} / {max || "?"} {exhausted && <span className="text-danger">— maximum atteint</span>}
              </p>
            </Section>
            {str(details.sha) && (
              <Section title="Commit relu">
                <code className="font-mono text-xs">{str(details.sha).slice(0, 12)}</code>
              </Section>
            )}
          </div>
        </Card>
      </div>
      <DecisionBar
        summary={
          <>
            <b className="text-fg">{kept}</b> constat{kept > 1 ? "s" : ""} gardé
            {kept > 1 ? "s" : ""} sur {items.length}
            {draft.remark.trim() && " · 1 remarque"}
          </>
        }
      >
        <Button variant="danger" onClick={() => setAbandon(true)} disabled={m.isPending} icon={<X className="h-4 w-4" aria-hidden />}>
          Abandonner le ticket
        </Button>
        <Button
          variant="primary"
          data-testid="approve-button"
          onClick={() => decide("approved", comment, exhausted ? "Ticket bloqué." : "Développeur relancé.")}
          disabled={m.isPending || (!exhausted && items.length > 0 && kept === 0 && !draft.remark.trim())}
          loading={m.isPending && m.variables?.decision === "approved"}
          icon={<Send className="h-4 w-4" aria-hidden />}
        >
          {exhausted ? "Bloquer le ticket" : `Renvoyer au développeur (tentative ${attempt + 1} / ${max || "?"})`}
        </Button>
      </DecisionBar>
      <ConfirmDialog
        open={abandon}
        onClose={() => setAbandon(false)}
        onConfirm={() => {
          setAbandon(false);
          decide("rejected", draft.remark.trim() || "Ticket abandonné depuis la reprise après revue.", "Ticket abandonné.");
        }}
        title="Abandonner ce ticket ?"
        description="Le ticket est annulé : il ne sera ni corrigé ni publié."
        confirmLabel="Abandonner le ticket"
        tone="danger"
      />
    </div>
  );
}

export function StructuredReview({ a }: { a: Approval }) {
  if (a.kind === "spec_review") return <SpecReview a={a} />;
  if (a.kind === "design_review") return <DesignReview a={a} />;
  if (a.kind === "rework") return <ReworkReview a={a} />;
  return null;
}
