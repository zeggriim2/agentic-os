import { describe, expect, it } from "vitest";
import type { GitCommit, GitKind } from "@/api/types";
import { layoutGraph } from "@/lib/gitGraph";

const c = (sha: string, parents: string[], kind: GitKind = "main"): GitCommit => ({
  sha,
  parents,
  kind,
  branch: kind,
  date: "2026-10-27T10:00:00Z",
  author: "a",
  subject: sha,
  url: `u/${sha}`,
});

describe("layoutGraph", () => {
  it("garde une histoire linéaire sur un seul couloir", () => {
    const l = layoutGraph([c("c3", ["c2"]), c("c2", ["c1"]), c("c1", [])]);
    expect(l.width).toBe(1);
    expect(l.rows.map((r) => r.lane)).toEqual([0, 0, 0]);
    expect(l.rows[0]!.segments).toEqual([{ from: 0, to: 0, type: "out", kind: "main" }]);
  });

  it("ouvre un couloir pour la branche mergée et le referme à la base commune", () => {
    // M = merge de F dans main ; F part de B ; A est le parent de B.
    const l = layoutGraph([c("M", ["B", "F"]), c("F", ["B"], "feature"), c("B", ["A"]), c("A", [])]);
    const [m, f, b] = l.rows;
    expect(m!.lane).toBe(0);
    expect(m!.segments).toEqual([
      { from: 0, to: 0, type: "out", kind: "main" },
      { from: 0, to: 1, type: "out", kind: "feature" },
    ]);
    expect(f!.lane).toBe(1);
    // F garde sa colonne jusqu'à la base commune B, où les deux couloirs convergent.
    expect(f!.segments).toContainEqual({ from: 1, to: 1, type: "out", kind: "feature" });
    expect(f!.segments).toContainEqual({ from: 0, to: 0, type: "through", kind: "main" });
    expect(b!.lane).toBe(0);
    expect(b!.segments.filter((s) => s.type === "in")).toEqual([
      { from: 0, to: 0, type: "in", kind: "main" },
      { from: 1, to: 0, type: "in", kind: "feature" },
    ]);
    expect(l.width).toBe(2);
  });

  it("fait converger deux branches à leur base commune, chacune dans sa couleur", () => {
    const l = layoutGraph([c("X", ["B"], "develop"), c("Y", ["B"], "feature"), c("B", [])]);
    expect(l.rows[1]!.lane).toBe(1);
    const b = l.rows[2]!;
    expect(b.lane).toBe(0);
    expect(b.segments).toEqual([
      { from: 0, to: 0, type: "in", kind: "develop" },
      { from: 1, to: 0, type: "in", kind: "feature" },
    ]);
    expect(l.width).toBe(2);
  });

  it("fait converger un merge et sa branche quand elles attendent le même commit", () => {
    // M merge F ; F et M attendent tous deux B : B reçoit les deux couloirs s'ils sont distincts.
    const l = layoutGraph([c("M", ["A", "F"]), c("F", ["B"], "feature"), c("A", ["B"]), c("B", [])]);
    const b = l.rows[3]!;
    expect(b.lane).toBe(0);
    expect(b.segments.filter((s) => s.type === "in").map((s) => s.from).sort()).toEqual([0, 1]);
  });

  it("marque d'un trait pointillé un parent hors de la période", () => {
    const l = layoutGraph([c("N", ["hors-periode"])]);
    expect(l.rows[0]!.segments).toEqual([{ from: 0, to: 0, type: "stub", kind: "main" }]);
  });

  it("réutilise un couloir libéré", () => {
    const l = layoutGraph([c("M", ["B", "F"]), c("F", ["B"], "feature"), c("B", ["A"]), c("G", ["A"], "feature"), c("A", [])]);
    // Le couloir 1 libéré par F est repris par G.
    expect(l.rows[3]!.lane).toBe(1);
    expect(l.width).toBe(2);
  });
});
