import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { api, buildQuery, type QueryValue } from "./client";
import type {
  Agent,
  AgentPatch,
  Approval,
  ApprovalDecision,
  AuditEntry,
  Feature,
  FeatureCreate,
  FeatureDetail,
  Health,
  Overview,
  Page,
  Pipeline,
  Project,
  ProjectCreate,
  ProjectPatch,
  Run,
  RunDetail,
  TicketDetail,
  GitGraph,
  HermesProfile,
} from "./types";

// Clés de requête — préfixes utilisés pour l'invalidation SSE.
export const qk = {
  me: ["me"] as const,
  overview: ["overview"] as const,
  pipeline: ["pipeline"] as const,
  projects: ["projects"] as const,
  project: (id: number) => ["project", id] as const,
  gitGraph: (id: number, days: number, kinds: string, merged: boolean) =>
    ["git-graph", id, days, kinds, merged] as const,
  agents: ["agents"] as const,
  hermesProfiles: ["hermes-profiles"] as const,
  features: (p: Record<string, QueryValue> = {}) => ["features", p] as const,
  feature: (id: number) => ["feature", id] as const,
  ticket: (id: number) => ["ticket", id] as const,
  runs: (p: Record<string, QueryValue> = {}) => ["runs", p] as const,
  run: (id: number) => ["run", id] as const,
  approvals: (p: Record<string, QueryValue> = {}) => ["approvals", p] as const,
  approval: (id: number) => ["approval", id] as const,
  audit: (p: Record<string, QueryValue> = {}) => ["audit", p] as const,
  health: ["health"] as const,
};

/** Accepte une liste paginée {items,next_cursor} ou (tolérance) un tableau brut. */
export function asPage<T>(raw: unknown): Page<T> {
  if (Array.isArray(raw)) return { items: raw as T[], next_cursor: null };
  const r = (raw ?? {}) as Partial<Page<T>>;
  return {
    items: Array.isArray(r.items) ? r.items : [],
    next_cursor: r.next_cursor ?? null,
  };
}

/**
 * Le contrat dit « feature + spec + design + tickets + … » sans préciser l'imbrication :
 * on accepte un objet plat ou {feature:{…}, tickets, …}.
 */
export function flattenDetail<T>(raw: unknown, key: string): T {
  if (raw && typeof raw === "object" && key in raw) {
    const r = raw as Record<string, unknown>;
    const inner = r[key];
    if (inner && typeof inner === "object") {
      const rest: Record<string, unknown> = { ...r };
      delete rest[key];
      return { ...(inner as object), ...rest } as T;
    }
  }
  return raw as T;
}

function withArrays<T extends object>(obj: T, keys: string[]): T {
  const o = obj as Record<string, unknown>;
  for (const k of keys) if (!Array.isArray(o[k])) o[k] = [];
  return obj;
}

/**
 * Lit toutes les pages d'une liste (curseur) : aucune troncature silencieuse. Borne de
 * sécurité : au-delà de maxPages, une erreur est levée plutôt que de masquer des éléments.
 */
export async function fetchAllPages<T>(
  path: string,
  params: Record<string, QueryValue>,
  signal?: AbortSignal,
  maxPages = 100,
): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | null = null;
  for (let i = 0; i < maxPages; i++) {
    const page: Page<T> = asPage<T>(
      await api.get(
        `${path}${buildQuery({ ...params, limit: 200, cursor })}`,
        signal,
      ),
    );
    out.push(...page.items);
    if (!page.next_cursor) return out;
    cursor = page.next_cursor;
  }
  throw new Error(`Liste ${path} trop longue (plus de ${maxPages} pages).`);
}

// ---------- Lectures ----------
export const useOverview = () =>
  useQuery({
    queryKey: qk.overview,
    queryFn: ({ signal }) => api.get<Overview>("/api/overview", signal),
  });

export const usePipeline = () =>
  useQuery({
    queryKey: qk.pipeline,
    queryFn: ({ signal }) => api.get<Pipeline>("/api/pipeline", signal),
  });

export const useProjects = () =>
  useQuery({
    queryKey: qk.projects,
    queryFn: ({ signal }) =>
      fetchAllPages<Project>("/api/projects", {}, signal),
  });

export const useGitGraph = (
  id: number,
  days: number,
  kinds: string[],
  merged: boolean,
) =>
  useQuery({
    queryKey: qk.gitGraph(id, days, kinds.join(","), merged),
    queryFn: ({ signal }) =>
      api.get<GitGraph>(
        `/api/projects/${id}/git-graph${buildQuery({ days, kinds: kinds.join(","), merged: merged ? 1 : undefined })}`,
        signal,
      ),
    enabled: Number.isFinite(id) && id > 0 && kinds.length > 0,
    staleTime: 30_000,
  });

export const useAgents = () =>
  useQuery({
    queryKey: qk.agents,
    queryFn: async ({ signal }) =>
      asPage<Agent>(await api.get("/api/agents", signal)).items,
  });

export const useHermesProfiles = (enabled = true) =>
  useQuery({
    queryKey: qk.hermesProfiles,
    queryFn: async ({ signal }) =>
      asPage<HermesProfile>(await api.get("/api/hermes-profiles", signal))
        .items,
    enabled,
    staleTime: 30_000,
  });

function useInfiniteList<T>(
  key: readonly unknown[],
  path: string,
  params: Record<string, QueryValue>,
  refetchInterval?: number,
) {
  return useInfiniteQuery({
    queryKey: key,
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam, signal }) =>
      asPage<T>(
        await api.get(
          `${path}${buildQuery({ ...params, limit: 50, cursor: pageParam })}`,
          signal,
        ),
      ),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval,
  });
}

export const useFeatures = (p: Record<string, QueryValue>) =>
  useInfiniteList<Feature>(qk.features(p), "/api/features", p);
export const useRuns = (p: Record<string, QueryValue>) =>
  useInfiniteList<Run>(qk.runs(p), "/api/runs", p);
export const useAudit = (p: Record<string, QueryValue>) =>
  useInfiniteList<AuditEntry>(qk.audit(p), "/api/audit", p);

export const useApprovals = (p: Record<string, QueryValue>) =>
  useQuery({
    queryKey: qk.approvals(p),
    queryFn: ({ signal }) =>
      fetchAllPages<Approval>("/api/approvals", p, signal),
  });

export const useFeature = (id: number) =>
  useQuery({
    queryKey: qk.feature(id),
    queryFn: async ({ signal }) =>
      withArrays(
        flattenDetail<FeatureDetail>(
          await api.get(`/api/features/${id}`, signal),
          "feature",
        ),
        ["tickets", "approvals", "runs", "timeline"],
      ),
  });

export const useTicket = (id: number) =>
  useQuery({
    queryKey: qk.ticket(id),
    queryFn: async ({ signal }) =>
      withArrays(
        flattenDetail<TicketDetail>(
          await api.get(`/api/tickets/${id}`, signal),
          "ticket",
        ),
        ["runs", "approvals", "timeline"],
      ),
  });

export const useRun = (id: number, enabled = true) =>
  useQuery({
    enabled,
    queryKey: qk.run(id),
    queryFn: async ({ signal }) =>
      withArrays(
        flattenDetail<RunDetail>(
          await api.get(`/api/runs/${id}`, signal),
          "run",
        ),
        ["events"],
      ),
  });

export const useApproval = (id: number, opts: { poll?: boolean } = {}) =>
  useQuery({
    queryKey: qk.approval(id),
    queryFn: async ({ signal }) =>
      flattenDetail<Approval>(
        await api.get(`/api/approvals/${id}`, signal),
        "approval",
      ),
    // Après « approuvé », l'exécution est asynchrone : on relit jusqu'à executed/execution_failed.
    refetchInterval: (q) =>
      opts.poll && q.state.data?.status === "approved" ? 2000 : false,
  });

export const useHealth = () =>
  useQuery({
    queryKey: qk.health,
    queryFn: ({ signal }) => api.get<Health>("/api/system/health", signal),
    refetchInterval: 30_000,
  });

// ---------- Écritures ----------
function invalidateMany(qc: QueryClient, keys: string[]) {
  return Promise.all(keys.map((k) => qc.invalidateQueries({ queryKey: [k] })));
}

export function useFeatureAction(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (action: "start_spec" | "cancel" | "retry") =>
      api.post(`/api/features/${id}/actions`, { action }),
    onSettled: () =>
      invalidateMany(qc, [
        "feature",
        "features",
        "pipeline",
        "overview",
        "runs",
      ]),
  });
}

export function useTicketAction(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (action: "cancel" | "retry") =>
      api.post(`/api/tickets/${id}/actions`, { action }),
    onSettled: () =>
      invalidateMany(qc, ["ticket", "feature", "pipeline", "overview", "runs"]),
  });
}

export function useCreateFeature() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: FeatureCreate) =>
      api.post<Feature>("/api/features", body),
    onSuccess: () => invalidateMany(qc, ["features", "pipeline", "overview"]),
  });
}

export function useStopRun(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post(`/api/runs/${id}/stop`),
    onSettled: () =>
      invalidateMany(qc, ["run", "runs", "overview", "feature", "ticket"]),
  });
}

export function useDecideApproval(id: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ApprovalDecision) =>
      api.post(`/api/approvals/${id}/decision`, body),
    // Toujours relire l'approbation depuis l'API (succès comme 409 « déjà décidée »).
    onSettled: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: qk.approval(id) }),
        invalidateMany(qc, [
          "approvals",
          "overview",
          "pipeline",
          "feature",
          "ticket",
        ]),
      ]),
  });
}

export function useSetDispatch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (paused: boolean) =>
      api.post("/api/system/dispatch", { paused }),
    onSettled: () => invalidateMany(qc, ["overview", "health"]),
  });
}

export function usePatchAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ role, patch }: { role: string; patch: AgentPatch }) =>
      api.patch<Agent>(`/api/agents/${role}`, patch),
    onSettled: () => qc.invalidateQueries({ queryKey: qk.agents }),
  });
}

export function useSyncAgents() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post("/api/agents/sync"),
    onSettled: () => invalidateMany(qc, ["agents", "overview"]),
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProjectCreate) =>
      api.post<Project>("/api/projects", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.projects }),
  });
}

export function useTestProjectTelegram() {
  return useMutation({
    mutationFn: (id: number) =>
      api.post<{ sent: boolean }>(`/api/projects/${id}/telegram-test`, {}),
  });
}

export function usePatchProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, patch }: { id: number; patch: ProjectPatch }) =>
      api.patch<Project>(`/api/projects/${id}`, patch),
    onSettled: () => invalidateMany(qc, ["projects", "project"]),
  });
}
