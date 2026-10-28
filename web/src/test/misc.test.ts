import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
  applyInvalidation,
  backoffDelay,
  INVALIDATION_MAP,
} from "@/api/events";
import { SSE_EVENT_TYPES } from "@/api/types";
import { diffKeys } from "@/components/domain";
import { normalizeHealth } from "@/pages/Health";
import { telegramLink, validateProject } from "@/pages/Projects";

describe("SSE", () => {
  it("chaque type d'événement du contrat a une règle d'invalidation", () => {
    for (const t of SSE_EVENT_TYPES) expect(INVALIDATION_MAP[t]).toBeDefined();
    expect(INVALIDATION_MAP["approval.updated"]).toContain("approval");
    expect(INVALIDATION_MAP["overview.invalidate"]).toEqual(["overview"]);
  });

  it("invalide les requêtes ciblées", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    applyInvalidation(qc, "feature.updated");
    const keys = spy.mock.calls.map(
      (c) => (c[0] as { queryKey: string[] }).queryKey[0],
    );
    expect(keys).toEqual(
      expect.arrayContaining(["feature", "features", "pipeline", "overview"]),
    );
  });

  it("backoff exponentiel plafonné à ~30 s", () => {
    expect(backoffDelay(0)).toBeGreaterThanOrEqual(800);
    expect(backoffDelay(0)).toBeLessThanOrEqual(1200);
    expect(backoffDelay(3)).toBeGreaterThanOrEqual(6400);
    expect(backoffDelay(20)).toBeLessThanOrEqual(36000);
  });
});

describe("utilitaires d'écran", () => {
  it("diff before/after de premier niveau", () => {
    expect(
      diffKeys(
        { state: "SPEC", v: 1, same: true },
        { state: "SPEC_REVIEW", v: 2, same: true },
      ),
    ).toEqual([
      { key: "state", before: "SPEC", after: "SPEC_REVIEW" },
      { key: "v", before: 1, after: 2 },
    ]);
    expect(diffKeys(null, null)).toEqual([]);
  });

  it("normalise les composants de santé", () => {
    expect(normalizeHealth("ok")).toEqual({ status: "ok" });
    expect(normalizeHealth(false)).toEqual({ status: "error" });
    expect(normalizeHealth({ status: "degraded", message: "lent" })).toEqual({
      status: "degraded",
      message: "lent",
    });
    expect(normalizeHealth({ ok: true })).toEqual({
      status: "ok",
      message: undefined,
    });
  });

  it("valide le formulaire projet", () => {
    const ok = {
      slug: "shop",
      name: "Shop",
      repository: "acme/shop",
      base_branch: "main",
      required_checks: "",
      mode: "sandbox" as const,
      test_command: "",
      profile_developer: "",
      profile_reviewer: "",
      profile_qa: "",
      telegram_chat: "",
      telegram_thread: "",
    };
    expect(validateProject(ok, true)).toEqual({});
    expect(
      validateProject(
        { ...ok, profile_developer: "aos-fraiszen-developer" },
        true,
      ),
    ).toEqual({});
    expect(
      validateProject({ ...ok, profile_qa: "Aos QA" }, true),
    ).toMatchObject({ profile_qa: expect.any(String) });
    expect(
      validateProject({ ...ok, slug: "Shop!", repository: "acme" }, true),
    ).toMatchObject({
      slug: expect.any(String),
      repository: expect.any(String),
    });
    expect(
      validateProject(
        { ...ok, telegram_chat: "-1004205411589", telegram_thread: "13" },
        true,
      ),
    ).toEqual({});
    expect(
      validateProject({ ...ok, telegram_chat: "@groupe" }, true),
    ).toMatchObject({ telegram_chat: expect.any(String) });
    expect(
      validateProject({ ...ok, telegram_thread: "13" }, true),
    ).toMatchObject({ telegram_thread: expect.any(String) });
    expect(
      validateProject(
        { ...ok, telegram_chat: "-100", telegram_thread: "0" },
        true,
      ),
    ).toMatchObject({
      telegram_thread: expect.any(String),
    });
  });

  it("construit le lien Telegram d'un groupe ou d'un sujet", () => {
    expect(telegramLink("-1004205411589", "13")).toBe(
      "https://t.me/c/4205411589/13",
    );
    expect(telegramLink("-1004205411589", "")).toBe(
      "https://t.me/c/4205411589",
    );
    expect(telegramLink("6923564552", "")).toBeNull();
  });
});
