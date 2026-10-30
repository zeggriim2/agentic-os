import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { setCsrfToken } from "@/api/client";
import type { ProfileModels } from "@/api/types";
import { ProfileModelsSection } from "@/pages/ProfileModels";
import { mockFetch, renderWithProviders } from "./utils";

const catalog = {
  items: [
    { provider: "anthropic", name: "Anthropic", is_current: false, models: ["claude-opus-5-5"] },
    { provider: "openai-codex", name: "OpenAI Codex", is_current: true, models: ["gpt-5.6-luna-900k"] },
  ],
  next_cursor: null,
};
const codex = { provider: "openai-codex", model: "gpt-5.6-luna-900k" };
const claude = { provider: "anthropic", model: "claude-opus-5-5" };

const data = (over: Partial<ProfileModels> = {}): ProfileModels => ({
  items: [
    {
      profile: "aos-fraiszen-developer",
      role: "developer",
      usages: [{ project: "fraiszen", role: "developer" }],
      current: claude,
      desired: claude,
      state: "APPLIED",
      error: null,
      revision: 1,
      requested_at: "2026-10-30T08:00:00Z",
      applied_at: "2026-10-30T08:00:20Z",
      matches_desired: true,
    },
    {
      profile: "aos-po",
      role: "po",
      usages: [{ project: "", role: "po" }],
      current: codex,
      desired: null,
      state: "",
      error: null,
      revision: 0,
      requested_at: null,
      applied_at: null,
      matches_desired: false,
    },
    {
      profile: "aos-qa",
      role: "qa",
      usages: [],
      current: codex,
      desired: claude,
      state: "FAILED",
      error: "profil introuvable sur l'hôte",
      revision: 2,
      requested_at: "2026-10-30T08:00:00Z",
      applied_at: null,
      matches_desired: false,
    },
  ],
  mismatches: [{ project: "fraiszen", role: "po", profile: "aos-po", requested: claude, current: codex }],
  sync: { last_seen_at: "2026-10-30T08:00:30Z", host: "srv", stale: false },
  ...over,
});

describe("Modèle par défaut des profils", () => {
  beforeEach(() => setCsrfToken("csrf"));

  it("affiche modèle actuel, demande, échec et écart de fournisseur", async () => {
    mockFetch({ "GET /api/profile-models": { body: data() }, "GET /api/hermes/models": { body: catalog } });
    renderWithProviders(<ProfileModelsSection />);
    const dev = within(await screen.findByTestId("profile-aos-fraiszen-developer"));
    expect(dev.getByText("anthropic/claude-opus-5-5", { selector: "td:nth-child(2) *" })).toBeInTheDocument();
    expect(dev.getByText("Appliqué")).toBeInTheDocument();
    expect(dev.getByText(/fraiszen · Développeur/)).toBeInTheDocument();
    const qa = within(screen.getByTestId("profile-aos-qa"));
    expect(qa.getByText("Échec")).toBeInTheDocument();
    expect(qa.getByText("profil introuvable sur l'hôte")).toBeInTheDocument();
    expect(screen.getByTestId("profile-mismatches")).toHaveTextContent(
      "fraiszen · Product Owner : demande anthropic/claude-opus-5-5, profil aos-po sur openai-codex/gpt-5.6-luna-900k",
    );
    expect(screen.queryByText(/script de synchronisation/)).not.toBeInTheDocument();
  });

  it("enregistre un nouveau modèle depuis la liste déroulante, après confirmation", async () => {
    const { calls } = mockFetch({
      "GET /api/profile-models": { body: data() },
      "GET /api/hermes/models": { body: catalog },
      "PUT /api/profile-models/aos-po": { body: { profile: "aos-po", state: "PENDING", revision: 1 } },
    });
    const user = userEvent.setup();
    renderWithProviders(<ProfileModelsSection />);
    const row = within(await screen.findByTestId("profile-aos-po"));
    const apply = row.getByRole("button", { name: "Appliquer" });
    expect(apply).toBeDisabled();
    const select = row.getByRole("combobox", { name: "Nouveau modèle pour aos-po" });
    await waitFor(() => expect(within(select).getByRole("option", { name: "claude-opus-5-5" })).toBeInTheDocument());
    await user.selectOptions(select, "openai-codex/gpt-5.6-luna-900k");
    expect(apply).toBeDisabled(); // déjà le modèle actuel
    await user.selectOptions(select, "anthropic/claude-opus-5-5");
    await user.click(apply);
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("openai-codex/gpt-5.6-luna-900k → anthropic/claude-opus-5-5");
    await user.click(within(dialog).getByRole("button", { name: "Changer" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.url).toBe("/api/profile-models/aos-po");
    expect(put.body).toEqual(claude);
  });

  it("aligne un profil depuis l'écart et signale un script absent", async () => {
    const { calls } = mockFetch({
      "GET /api/profile-models": { body: data({ sync: { last_seen_at: null, host: "", stale: true } }) },
      "GET /api/hermes/models": { body: catalog },
      "PUT /api/profile-models/aos-po": { body: { profile: "aos-po", state: "PENDING", revision: 1 } },
    });
    const user = userEvent.setup();
    renderWithProviders(<ProfileModelsSection />);
    expect(await screen.findByText(/script de synchronisation n'a jamais tourné/)).toBeInTheDocument();
    expect(screen.getByText("deploy/agents/install-profile-sync.sh")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Aligner le profil" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Changer" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual(claude));
  });
});
