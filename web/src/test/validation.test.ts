import { describe, expect, it } from "vitest";
import cases from "./fixtures/validation-cases.json";
import {
  LIMITS,
  validBranch,
  validCheckName,
  validDesiredModel,
  validFeatureDescription,
  validFeatureTitle,
  validMaxTokens,
  validProfile,
  validProvider,
  validTelegramChat,
  validProjectName,
  validRepository,
  validSlug,
  validTestCommand,
} from "@/lib/validation";

type Value = string | { repeat: string; count: number };
const value = (v: Value) =>
  typeof v === "string" ? v : v.repeat.repeat(v.count);

const rules: Record<string, (s: string) => boolean> = {
  slug: validSlug,
  repository: validRepository,
  branch: validBranch,
  project_name: validProjectName,
  test_command: validTestCommand,
  required_check: validCheckName,
  feature_title: validFeatureTitle,
  feature_description: validFeatureDescription,
  model: validDesiredModel,
  profile: validProfile,
  provider: validProvider,
  telegram_chat: validTelegramChat,
};

// Même fichier que internal/domain/validation_test.go : l'UI et l'API ne peuvent pas diverger.
describe("contrat de validation partagé avec l'API", () => {
  const all = cases as unknown as Record<
    string,
    { valid: Value[]; invalid: Value[] }
  >;
  for (const [name, rule] of Object.entries(rules)) {
    it(`${name} : mêmes cas valides et invalides que le backend`, () => {
      for (const v of all[name]!.valid)
        expect(rule(value(v)), `${name} valide: ${JSON.stringify(v)}`).toBe(
          true,
        );
      for (const v of all[name]!.invalid)
        expect(rule(value(v)), `${name} invalide: ${JSON.stringify(v)}`).toBe(
          false,
        );
    });
  }
  it("budget de tokens et nombre de checks", () => {
    for (const n of cases.max_tokens.valid)
      expect(validMaxTokens(n)).toBe(true);
    for (const n of cases.max_tokens.invalid)
      expect(validMaxTokens(n)).toBe(false);
    expect(LIMITS.requiredChecks).toBe(cases.required_checks_max);
  });
});
