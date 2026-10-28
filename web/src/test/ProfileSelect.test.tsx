import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ProfileSelect } from "@/pages/Projects";
import type { HermesProfile } from "@/api/types";

const profiles: HermesProfile[] = [
  { profile: "aos-developer", role: "developer" },
  { profile: "aos-fraiszen-developer", role: "developer" },
  { profile: "aos-fraiszen-qa", role: "qa" },
  { profile: "perso", role: "" },
];

const options = () => screen.getAllByRole("option").map((o) => o.textContent);

describe("ProfileSelect", () => {
  it("propose le défaut, les profils du rôle puis les autres", () => {
    render(
      <ProfileSelect
        role="developer"
        value=""
        onChange={() => {}}
        defaultProfile="aos-developer"
        profiles={profiles}
      />,
    );
    expect(options()).toEqual([
      "Par défaut (aos-developer)",
      "aos-fraiszen-developer",
      "aos-fraiszen-qa",
      "perso",
    ]);
    expect(screen.getByRole("group", { name: "Autres profils" })).toBeTruthy();
  });

  it("renvoie la valeur choisie, vide pour le défaut", () => {
    const onChange = vi.fn();
    render(
      <ProfileSelect
        role="developer"
        value="aos-fraiszen-developer"
        onChange={onChange}
        defaultProfile="aos-developer"
        profiles={profiles}
      />,
    );
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("");
  });

  it("garde visible un profil enregistré dont la clé a disparu", () => {
    render(
      <ProfileSelect
        role="qa"
        value="aos-ancien-qa"
        onChange={() => {}}
        defaultProfile="aos-qa"
        profiles={profiles}
      />,
    );
    expect(options()).toContain("aos-ancien-qa — clé absente");
    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe(
      "aos-ancien-qa",
    );
  });
});
