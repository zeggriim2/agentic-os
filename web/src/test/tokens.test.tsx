import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TokenChart, daySplit } from "@/components/ui/TokenChart";
import { newTokens, runTokenSplit } from "@/lib/tokens";

describe("répartition des tokens", () => {
  it("calcule l'entrée nouvelle à partir du cache, borné à l'entrée", () => {
    expect(runTokenSplit({ input_tokens: 305443, output_tokens: 2637, total_tokens: 308080, cache_read_tokens: 248960 })).toEqual({
      fresh: 56483, cache: 248960, output: 2637, total: 308080,
    });
    expect(runTokenSplit({ input_tokens: 10, output_tokens: 1, total_tokens: 11, cache_read_tokens: 99 })).toMatchObject({ fresh: 0, cache: 10 });
    expect(runTokenSplit({ input_tokens: null, output_tokens: null, total_tokens: null })).toEqual({ fresh: 0, cache: 0, output: 0, total: 0 });
    expect(newTokens({ fresh: 56483, output: 2637 })).toBe(59120);
  });

  it("empile nouveaux et cache par jour, avec repli sans répartition", () => {
    expect(daySplit({ day: "2026-10-27", total: 308080, fresh_input: 56483, cache_read: 248960, output: 2637 })).toEqual({ fresh: 59120, cache: 248960 });
    expect(daySplit({ day: "2026-10-27", total: 500 })).toEqual({ fresh: 500, cache: 0 });
    const { container } = render(
      <TokenChart
        data={[
          { day: "2026-10-26", total: 0, fresh_input: 0, cache_read: 0, output: 0 },
          { day: "2026-10-27", total: 308080, fresh_input: 56483, cache_read: 248960, output: 2637 },
        ]}
      />,
    );
    expect(container.querySelectorAll('[data-part="fresh"]')).toHaveLength(1);
    expect(container.querySelectorAll('[data-part="cache"]')).toHaveLength(1);
    expect(screen.getByRole("img").getAttribute("aria-label")?.replace(/\s/g, " ")).toContain("59 120 nouveaux et 248 960 relus du cache");
    expect(screen.getByText(/relus du cache :/)).toBeInTheDocument();
  });
});
