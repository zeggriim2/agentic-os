// Logique des pages de décision (1.13) : questions ouvertes des agents, réponses de l'humain,
// brouillon local et composition du retour transmis à l'agent. Sans React : testable seule.

export interface OpenQuestion {
  question: string;
  options: string[];
  /** Index de la réponse recommandée par l'agent (null si aucune). */
  recommended: number | null;
}

export interface Answer {
  /** Réponse proposée choisie (index dans options) ou null. */
  choice: number | null;
  /** Réponse ou précision libre. */
  text: string;
  /** L'humain laisse l'agent trancher. */
  delegate: boolean;
  /** Message « Demander à l'agent » envoyé dans le fil d'équipe (id), pour afficher sa réponse. */
  askedId?: number;
}

export interface Draft {
  answers: Record<number, Answer>;
  /** Remarques sur un élément précis (critère, ticket…), indexées par clé libre (ex. « c3 », « t1 »). */
  notes: Record<string, string>;
  /** Constats écartés (reprise après revue), par index. */
  dropped: number[];
  /** Remarque générale. */
  remark: string;
}

export const emptyDraft = (): Draft => ({
  answers: {},
  notes: {},
  dropped: [],
  remark: "",
});
export const emptyAnswer = (): Answer => ({
  choice: null,
  text: "",
  delegate: false,
});

/** Accepte l'ancien format (chaîne) comme le nouveau ({question, options, recommended}). */
export function parseQuestions(v: unknown): OpenQuestion[] {
  if (!Array.isArray(v)) return [];
  const out: OpenQuestion[] = [];
  for (const it of v) {
    if (typeof it === "string") {
      if (it.trim()) out.push({ question: it, options: [], recommended: null });
      continue;
    }
    if (!it || typeof it !== "object") continue;
    const o = it as Record<string, unknown>;
    if (typeof o.question !== "string" || !o.question.trim()) continue;
    const options = Array.isArray(o.options) ? o.options.filter((x): x is string => typeof x === "string" && x.trim() !== "") : [];
    const r =
      typeof o.recommended === "number" && Number.isInteger(o.recommended) && o.recommended >= 0 && o.recommended < options.length ? o.recommended : null;
    out.push({ question: o.question, options, recommended: r });
  }
  return out;
}

export function isAnswered(a: Answer | undefined): boolean {
  return !!a && (a.delegate || a.choice !== null || a.text.trim() !== "");
}

export function countAnswered(qs: OpenQuestion[], d: Draft): number {
  return qs.reduce((n, _q, i) => n + (isAnswered(d.answers[i]) ? 1 : 0), 0);
}

/** Texte de la réponse humaine à une question, tel que transmis à l'agent. */
export function answerText(q: OpenQuestion, a: Answer | undefined, agent: string): string {
  if (!a || !isAnswered(a)) return `sans réponse : tranche toi-même (${agent}) et indique ton choix.`;
  if (a.delegate) return `laissé à ta décision (${agent}).` + (a.text.trim() ? ` Précision : ${a.text.trim()}` : "");
  const parts: string[] = [];
  const chosen = a.choice !== null ? q.options[a.choice] : undefined;
  if (chosen !== undefined) parts.push(chosen);
  if (a.text.trim()) parts.push(a.text.trim());
  return parts.join(" — ");
}

export interface NoteRef {
  key: string;
  label: string;
}

/**
 * Retour structuré question → réponse transmis à l'agent (commentaire de la décision).
 * Chaque question apparaît, pour que l'agent n'en oublie aucune.
 */
export function composeFeedback(qs: OpenQuestion[], d: Draft, agent: string, noteRefs: NoteRef[] = []): string {
  const lines: string[] = [];
  if (qs.length) {
    lines.push("Réponses de l'humain à tes questions :");
    qs.forEach((q, i) => {
      lines.push(`${i + 1}. ${q.question}`);
      lines.push(`   → ${answerText(q, d.answers[i], agent)}`);
    });
  }
  const notes = noteRefs.filter((r) => d.notes[r.key]?.trim());
  if (notes.length) {
    if (lines.length) lines.push("");
    lines.push("Remarques sur des éléments précis :");
    for (const r of notes) lines.push(`- ${r.label} : ${(d.notes[r.key] ?? "").trim()}`);
  }
  if (d.remark.trim()) {
    if (lines.length) lines.push("");
    lines.push("Remarque générale : " + d.remark.trim());
  }
  return lines.join("\n");
}

/** Commentaire d'une reprise : constats écartés par l'humain et sa remarque. */
export function composeRework(items: string[], d: Draft): string {
  const lines: string[] = [];
  const dropped = d.dropped.filter((i) => i >= 0 && i < items.length).sort((a, b) => a - b);
  if (dropped.length) {
    lines.push("Constats écartés par l'humain (ne pas les traiter) :");
    for (const i of dropped) lines.push((items[i] ?? "").trim().replace(/^- /, "- ✗ "));
  }
  if (d.remark.trim()) {
    if (lines.length) lines.push("");
    lines.push(d.remark.trim());
  }
  return lines.join("\n");
}

/** Constats de reprise : liste structurée si présente, sinon découpage du texte ligne à ligne. */
export function findingItems(details: Record<string, unknown> | null): string[] {
  if (!details) return [];
  if (Array.isArray(details.finding_items)) return details.finding_items.filter((x): x is string => typeof x === "string" && x.trim() !== "");
  if (typeof details.findings === "string")
    return details.findings
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l !== "");
  return [];
}

const draftKey = (id: number) => `aos-approval-draft-${id}`;

export function loadDraft(id: number): Draft {
  try {
    const raw = localStorage.getItem(draftKey(id));
    if (!raw) return emptyDraft();
    const d = JSON.parse(raw) as Partial<Draft>;
    return {
      answers: d.answers ?? {},
      notes: d.notes ?? {},
      dropped: d.dropped ?? [],
      remark: d.remark ?? "",
    };
  } catch {
    return emptyDraft();
  }
}

export function saveDraft(id: number, d: Draft): void {
  try {
    localStorage.setItem(draftKey(id), JSON.stringify(d));
  } catch {
    /* stockage indisponible : le brouillon reste en mémoire */
  }
}

export function clearDraft(id: number): void {
  try {
    localStorage.removeItem(draftKey(id));
  } catch {
    /* ignoré */
  }
}

/** Corps du message « Demander à l'agent » sur une question précise. */
export function askBody(index: number, q: OpenQuestion, extra: string): string {
  let s = `À propos de ta question ${index + 1} (« ${q.question} »)`;
  if (q.options.length) s += `, options : ${q.options.map((o, i) => `${i + 1}) ${o}`).join(" ; ")}`;
  s += extra.trim() ? `. ${extra.trim()}` : ". Quelle réponse recommandes-tu et pourquoi ? Réponds en quelques phrases.";
  return s;
}
