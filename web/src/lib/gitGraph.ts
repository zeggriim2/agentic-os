import type { GitCommit, GitKind } from "@/api/types";

/** Couleurs par type de branche (SVG et légende). */
export const KIND_COLORS: Record<GitKind, string> = {
  main: "#3b82f6",
  develop: "#22c55e",
  release: "#f59e0b",
  hotfix: "#ef4444",
  feature: "#a855f7",
  other: "#94a3b8",
};

export const KIND_LABELS: Record<GitKind, string> = {
  main: "main",
  develop: "develop",
  release: "release/*",
  hotfix: "hotfix/*",
  feature: "features et tickets",
  other: "autres",
};

export const KIND_ORDER: GitKind[] = ["main", "develop", "release", "hotfix", "feature", "other"];

/** Segment d'une ligne : de la colonne `from` (haut ou nœud) vers la colonne `to` (bas). */
export interface Segment {
  from: number;
  to: number;
  /** "through" : traverse la ligne ; "in" : arrive sur le nœud ; "out" : part du nœud ; "stub" : parent hors graphe. */
  type: "through" | "in" | "out" | "stub";
  kind: GitKind;
}

export interface Row {
  commit: GitCommit;
  lane: number;
  segments: Segment[];
}

export interface Layout {
  rows: Row[];
  width: number;
}

/**
 * Place les commits (ordre topologique, enfants d'abord) sur des couloirs.
 * Chaque couloir « attend » un SHA : le commit prend le premier couloir qui l'attend, les autres
 * couloirs qui l'attendaient le rejoignent (fusion). Son premier parent continue toujours le même
 * couloir (une branche garde sa colonne jusqu'à sa base commune) ; les parents suivants (merge)
 * rejoignent le couloir qui les attend déjà, sinon un couloir libre. Un trait prend la couleur de
 * la branche qui l'a ouvert.
 */
export function layoutGraph(commits: GitCommit[]): Layout {
  const index = new Map(commits.map((c, i) => [c.sha, i]));
  const kindOf = (sha: string | null, fallback: GitKind): GitKind => {
    const i = sha ? index.get(sha) : undefined;
    return i === undefined ? fallback : commits[i]!.kind;
  };
  const lanes: (string | null)[] = [];
  const laneKinds: GitKind[] = [];
  const rows: Row[] = [];
  let width = 0;
  const freeLane = () => {
    const i = lanes.indexOf(null);
    if (i !== -1) return i;
    lanes.push(null);
    laneKinds.push("other");
    return lanes.length - 1;
  };

  for (const c of commits) {
    const segments: Segment[] = [];
    let lane = lanes.indexOf(c.sha);
    if (lane === -1) lane = freeLane();
    // Arrivées : tout couloir qui attendait ce commit converge vers son nœud.
    lanes.forEach((s, i) => {
      if (s === c.sha) {
        segments.push({ from: i, to: lane, type: "in", kind: laneKinds[i]! });
        lanes[i] = null;
      }
    });
    // Traversées : les autres couloirs actifs continuent tout droit.
    lanes.forEach((s, i) => {
      if (s !== null) segments.push({ from: i, to: i, type: "through", kind: laneKinds[i]! });
    });
    lanes[lane] = null;
    c.parents.forEach((p, n) => {
      if (!index.has(p)) {
        if (n === 0) segments.push({ from: lane, to: lane, type: "stub", kind: c.kind });
        return;
      }
      const existing = lanes.indexOf(p);
      const target = n === 0 ? lane : existing !== -1 ? existing : freeLane();
      const kind = n === 0 ? c.kind : kindOf(p, c.kind);
      if (lanes[target] !== p) laneKinds[target] = kind;
      lanes[target] = p;
      segments.push({ from: lane, to: target, type: "out", kind });
    });
    width = Math.max(width, lanes.length, lane + 1);
    rows.push({ commit: c, lane, segments });
  }
  return { rows, width };
}
