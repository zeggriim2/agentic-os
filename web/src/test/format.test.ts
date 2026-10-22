import { describe, expect, it } from "vitest";
import { formatDay, formatDuration, formatParis, formatParisShort, formatParisTime, formatRelative, shortSha } from "@/lib/format";

describe("formatage des dates (Europe/Paris)", () => {
  it("convertit l'UTC en heure de Paris en hiver (UTC+1)", () => {
    expect(formatParis("2026-01-15T12:00:00Z")).toBe("15/01/2026 13:00:00");
  });
  it("convertit l'UTC en heure de Paris en été (UTC+2)", () => {
    expect(formatParis("2026-07-01T12:00:00Z")).toBe("01/07/2026 14:00:00");
  });
  it("gère le changement de jour", () => {
    expect(formatParis("2026-10-21T23:30:00Z")).toBe("22/10/2026 01:30:00");
    expect(formatParisTime("2026-10-21T23:30:00Z")).toBe("01:30:00");
  });
  it("format court", () => {
    expect(formatParisShort("2026-10-21T08:05:00Z")).toMatch(/21 oct\.? 10:05/);
  });
  it("valeurs absentes ou invalides", () => {
    expect(formatParis(null)).toBe("—");
    expect(formatParis("pas une date")).toBe("—");
    expect(formatRelative(undefined)).toBe("—");
  });
  it("dates relatives en français", () => {
    const now = new Date("2026-10-21T12:00:00Z");
    expect(formatRelative("2026-10-21T11:59:50Z", now)).toBe("à l'instant");
    expect(formatRelative("2026-10-21T11:55:00Z", now)).toBe("il y a 5 minutes");
    expect(formatRelative("2026-10-21T09:00:00Z", now)).toBe("il y a 3 heures");
    expect(formatRelative("2026-10-20T12:00:00Z", now)).toBe("hier");
    expect(formatRelative("2026-10-21T14:00:00Z", now)).toBe("dans 2 heures");
  });
  it("durées, jours et SHA", () => {
    expect(formatDuration("2026-10-21T12:00:00Z", "2026-10-21T12:03:07Z")).toBe("3 min 07 s");
    expect(formatDuration("2026-10-21T12:00:00Z", "2026-10-21T13:05:00Z")).toBe("1 h 05 min");
    expect(formatDay("2026-10-21")).toMatch(/mer\.? 21/);
    expect(shortSha("0123456789abcdef")).toBe("0123456");
    expect(shortSha(null)).toBe("—");
  });
});
