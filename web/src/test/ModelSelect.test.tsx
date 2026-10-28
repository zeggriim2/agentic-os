import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  AgentModelsFields,
  ModelSelect,
  modelKey,
  parseModelKey,
} from "@/pages/Projects";
import { modelDrift } from "@/components/domain";
import type { ModelProvider } from "@/api/types";

const providers: ModelProvider[] = [
  {
    provider: "openai-codex",
    name: "OpenAI Codex",
    is_current: true,
    models: ["gpt-5.6-luna-900k", "gpt-6-sol"],
  },
  {
    provider: "anthropic",
    name: "Anthropic",
    is_current: false,
    models: ["claude-sonnet-5"],
  },
];

const options = () => screen.getAllByRole("option").map((o) => o.textContent);

describe("ModelSelect", () => {
  it("propose le défaut du profil puis les modèles groupés par fournisseur", () => {
    render(<ModelSelect value="" onChange={() => {}} providers={providers} />);
    expect(options()).toEqual([
      "Défaut du profil",
      "gpt-5.6-luna-900k",
      "gpt-6-sol",
      "claude-sonnet-5",
    ]);
    expect(
      screen.getByRole("group", { name: "OpenAI Codex (courant)" }),
    ).toBeTruthy();
    expect(screen.getByRole("group", { name: "Anthropic" })).toBeTruthy();
  });

  it("renvoie « fournisseur/modèle », vide pour le défaut", () => {
    const onChange = vi.fn();
    render(<ModelSelect value="" onChange={onChange} providers={providers} />);
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "anthropic/claude-sonnet-5" },
    });
    expect(onChange).toHaveBeenCalledWith("anthropic/claude-sonnet-5");
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("garde visible une valeur enregistrée absente du catalogue", () => {
    render(
      <ModelSelect
        value="anthropic/claude-ancien"
        onChange={() => {}}
        providers={providers}
      />,
    );
    expect(options()).toContain("anthropic/claude-ancien (absent)");
    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe(
      "anthropic/claude-ancien",
    );
  });
});

describe("AgentModelsFields", () => {
  it("affiche un sélecteur par rôle et remonte le rôle modifié", () => {
    const onChange = vi.fn();
    render(
      <AgentModelsFields
        values={{ developer: "anthropic/claude-sonnet-5" }}
        onChange={onChange}
        providers={providers}
      />,
    );
    const selects = screen.getAllByRole("combobox") as HTMLSelectElement[];
    expect(selects).toHaveLength(5);
    expect(
      (screen.getByLabelText("Modèle Développeur") as HTMLSelectElement).value,
    ).toBe("anthropic/claude-sonnet-5");
    fireEvent.change(screen.getByLabelText("Modèle QA"), {
      target: { value: "openai-codex/gpt-6-sol" },
    });
    expect(onChange).toHaveBeenCalledWith("qa", "openai-codex/gpt-6-sol");
  });

  it("reste utilisable quand le catalogue est indisponible", () => {
    render(
      <AgentModelsFields
        values={{ reviewer: "anthropic/claude-sonnet-5" }}
        onChange={() => {}}
        providers={[]}
        error="Hermes injoignable"
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain(
      "Catalogue des modèles indisponible : Hermes injoignable",
    );
    const reviewer = screen.getByLabelText("Modèle Reviewer") as HTMLSelectElement;
    expect(reviewer.value).toBe("anthropic/claude-sonnet-5");
    expect(Array.from(reviewer.options).map((o) => o.textContent)).toEqual([
      "Défaut du profil",
      "anthropic/claude-sonnet-5 (absent)",
    ]);
  });
});

describe("clés de modèle", () => {
  it("sérialise et relit « fournisseur/modèle » (le modèle peut contenir des /)", () => {
    expect(modelKey({ provider: "a", model: "org/m:1" })).toBe("a/org/m:1");
    expect(parseModelKey("a/org/m:1")).toEqual({ provider: "a", model: "org/m:1" });
    expect(modelKey(undefined)).toBe("");
    expect(parseModelKey("")).toBeUndefined();
    expect(parseModelKey("a/")).toBeUndefined();
    expect(parseModelKey("/m")).toBeUndefined();
  });
});

describe("modelDrift", () => {
  const base = { model: "gpt-6-sol", provider: "openai-codex" };
  it("rien sans surcharge demandée ou quand elle est servie", () => {
    expect(modelDrift({ ...base })).toBeNull();
    expect(
      modelDrift({ ...base, requested_model: "gpt-6-sol", requested_provider: "openai-codex" }),
    ).toBeNull();
  });
  it("signale demandé ≠ servi et le repli", () => {
    expect(
      modelDrift({ ...base, requested_model: "claude-sonnet-5", requested_provider: "anthropic" }),
    ).toEqual({ requested: "anthropic/claude-sonnet-5", fallback: false });
    expect(
      modelDrift({ ...base, requested_model: "gpt-6-sol", requested_provider: "openai-codex", model_fallback: true }),
    ).toEqual({ requested: "openai-codex/gpt-6-sol", fallback: true });
  });
});
