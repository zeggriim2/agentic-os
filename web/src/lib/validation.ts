// Règles de saisie identiques à l'API (internal/domain/validation.go).
// Contrat commun : src/test/fixtures/validation-cases.json, testé des deux côtés.
// Les longueurs sont comptées en caractères (points de code), comme côté Go.

export const LIMITS = {
  projectName: 200,
  testCommand: 500,
  requiredChecks: 20,
  checkName: 200,
  featureTitle: 200,
  featureDescription: 20000,
  minTokens: 1000,
  maxTokens: 50_000_000,
} as const;

const chars = (s: string) => [...s].length;

const SLUG = /^[a-z0-9][a-z0-9-]{0,39}$/;
const REPO = /^[A-Za-z0-9_.-]{1,100}\/[A-Za-z0-9_.-]{1,100}$/;
const BRANCH = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$/;
const MODEL = /^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$/;
const PROFILE = /^[a-z0-9][a-z0-9_-]{0,63}$/;
// eslint-disable-next-line no-control-regex
const CONTROL = /[\x00-\x1f]/;

export const validSlug = (s: string) => SLUG.test(s);
export const validRepository = (s: string) => REPO.test(s) && !s.includes("..");
export const validBranch = (s: string) => BRANCH.test(s) && !s.includes("..") && !s.endsWith("/") && !s.endsWith(".lock");
export const validProjectName = (s: string) => {
  const t = s.trim();
  return t !== "" && chars(t) <= LIMITS.projectName;
};
export const validTestCommand = (s: string) => chars(s.trim()) <= LIMITS.testCommand;
export const validCheckName = (s: string) => {
  const t = s.trim();
  return t !== "" && !CONTROL.test(t) && chars(t) <= LIMITS.checkName;
};
export const validFeatureTitle = (s: string) => {
  const t = s.trim();
  return t !== "" && chars(t) <= LIMITS.featureTitle;
};
export const validFeatureDescription = (s: string) => {
  const t = s.trim();
  return t !== "" && chars(t) <= LIMITS.featureDescription;
};
export const validDesiredModel = (s: string) => s === "" || MODEL.test(s);
export const validProfile = (s: string) => PROFILE.test(s);
export const validMaxTokens = (n: number) => Number.isInteger(n) && n >= LIMITS.minTokens && n <= LIMITS.maxTokens;
