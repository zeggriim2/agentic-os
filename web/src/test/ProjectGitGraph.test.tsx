import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import ProjectGitGraphPage from "@/pages/ProjectGitGraph";
import { mockFetch, renderWithProviders } from "./utils";

const project = {
  id: 3, slug: "sandbox", name: "Sandbox", repository: "o/s", base_branch: "main", required_checks: [], mode: "sandbox",
  enabled: true, test_command: null, created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z",
};
const web = "https://github.com/o/s";
const graph = {
  project, days: 30, since: "2026-09-27T00:00:00Z", truncated: false, merged: false, hidden_merged: 4,
  refs: [
    { name: "main", kind: "main", sha: "m2", date: "2026-10-27T10:00:00Z", url: `${web}/tree/main`, merged: false },
    { name: "aos/sandbox-F3-T1", kind: "feature", sha: "t1", date: "2026-10-27T09:00:00Z", url: `${web}/tree/aos/sandbox-F3-T1`, merged: false,
      ticket: { id: 11, key: "sandbox-F3-T1", state: "PR_OPENED" }, pr: { number: 14, url: `${web}/pull/14` } },
  ],
  tags: [{ name: "v1.0.1", sha: "m2", url: `${web}/releases/tag/v1.0.1` }],
  commits: [
    { sha: "t1", parents: ["m1"], date: "2026-10-27T09:00:00Z", author: "Dev", subject: "Ajouter la ligne TOTAL", branch: "aos/sandbox-F3-T1", kind: "feature", url: `${web}/commit/t1` },
    { sha: "m2", parents: ["m1", "x"], date: "2026-10-27T10:00:00Z", author: "L", subject: "Merge pull request #13 from o/b", branch: "main", kind: "main", url: `${web}/commit/m2`, pr: { number: 13, url: `${web}/pull/13` } },
    { sha: "m1", parents: [], date: "2026-10-20T10:00:00Z", author: "L", subject: "init", branch: "main", kind: "main", url: `${web}/commit/m1` },
  ],
};

describe("Graphe Git du projet", () => {
  beforeEach(() => localStorage.clear());

  it("affiche commits, branches, ticket, PR et tags avec leurs liens", async () => {
    const { calls } = mockFetch({ "GET /api/projects/3/git-graph": { body: graph } });
    renderWithProviders(<ProjectGitGraphPage />, { route: "/projects/3/git", path: "/projects/:id/git" });
    const rows = await screen.findAllByTestId("git-commit");
    expect(rows).toHaveLength(3);
    expect(screen.getByRole("heading", { name: "Graphe Git — Sandbox" })).toBeInTheDocument();
    expect(calls[0]!.url).toContain("days=30");
    expect(calls[0]!.url).toContain("kinds=main%2Cdevelop%2Crelease%2Chotfix%2Cfeature%2Cother");
    expect(calls[0]!.url).not.toContain("merged=");
    expect(screen.getByText("(4 masquées)")).toBeInTheDocument();

    const t = within(rows[0]!);
    expect(t.getByRole("link", { name: "aos/sandbox-F3-T1" })).toHaveAttribute("href", `${web}/tree/aos/sandbox-F3-T1`);
    expect(t.getByRole("link", { name: /sandbox-F3-T1 / })).toHaveAttribute("href", "/tickets/11");
    expect(t.getByRole("link", { name: "PR #14" })).toHaveAttribute("href", `${web}/pull/14`);

    const m = within(rows[1]!);
    expect(m.getByRole("link", { name: "v1.0.1" })).toHaveAttribute("href", `${web}/releases/tag/v1.0.1`);
    expect(m.getByRole("link", { name: "#13" })).toHaveAttribute("href", `${web}/pull/13`);
    expect(m.getByLabelText("commit de merge")).toBeInTheDocument();
    expect(m.getByRole("link", { name: "m2" })).toHaveAttribute("href", `${web}/commit/m2`);
    for (const a of screen.getAllByRole("link").filter((l) => l.getAttribute("href")?.startsWith("https://"))) {
      expect(a).toHaveAttribute("rel", "noreferrer noopener");
    }
  });

  it("filtre par période et par type, et mémorise les types", async () => {
    const { calls } = mockFetch({ "GET /api/projects/3/git-graph": { body: graph } });
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<ProjectGitGraphPage />, { route: "/projects/3/git", path: "/projects/:id/git" });
    await screen.findAllByTestId("git-commit");
    await user.selectOptions(screen.getByLabelText("Période"), "7");
    await user.click(screen.getByRole("checkbox", { name: /autres/ }));
    const last = calls[calls.length - 1]!.url;
    expect(last).toContain("days=7");
    expect(last).toContain("kinds=main%2Cdevelop%2Crelease%2Chotfix%2Cfeature");
    await user.click(screen.getByRole("checkbox", { name: /Branches déjà mergées/ }));
    expect(calls[calls.length - 1]!.url).toContain("merged=1");
    unmount();
    renderWithProviders(<ProjectGitGraphPage />, { route: "/projects/3/git", path: "/projects/:id/git" });
    expect(await screen.findByRole("checkbox", { name: /autres/ })).not.toBeChecked();
  });

  it("signale un graphe tronqué et une période vide", async () => {
    mockFetch({ "GET /api/projects/3/git-graph": { body: { ...graph, truncated: true } } });
    renderWithProviders(<ProjectGitGraphPage />, { route: "/projects/3/git", path: "/projects/:id/git" });
    expect(await screen.findByRole("status")).toHaveTextContent("Graphe tronqué aux 3 commits");
  });

  it("affiche un état vide sans commit", async () => {
    mockFetch({ "GET /api/projects/3/git-graph": { body: { ...graph, refs: [], tags: [], commits: [] } } });
    renderWithProviders(<ProjectGitGraphPage />, { route: "/projects/3/git", path: "/projects/:id/git" });
    expect(await screen.findByText("Aucune branche active")).toBeInTheDocument();
  });
});
