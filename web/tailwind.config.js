/** @type {import('tailwindcss').Config} */
const v = (name) => `rgb(var(--${name}) / <alpha-value>)`;

export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "Monaco", "Consolas", "Liberation Mono", "monospace"],
      },
      colors: {
        bg: v("bg"),
        panel: v("panel"),
        surface: v("surface"),
        elevated: v("elevated"),
        hover: v("hover"),
        border: v("border"),
        "border-strong": v("border-strong"),
        fg: v("fg"),
        "fg-muted": v("fg-muted"),
        "fg-subtle": v("fg-subtle"),
        accent: v("accent"),
        "accent-solid": v("accent-solid"),
        "accent-fg": v("accent-fg"),
        neutral: v("tone-neutral"),
        info: v("tone-info"),
        progress: v("tone-progress"),
        review: v("tone-review"),
        success: v("tone-success"),
        danger: v("tone-danger"),
        warning: v("tone-warning"),
      },
      fontSize: {
        "2xs": ["0.6875rem", { lineHeight: "1rem" }],
      },
      boxShadow: {
        pop: "0 8px 24px -6px rgb(0 0 0 / 0.35), 0 0 0 1px rgb(var(--border) / 1)",
      },
      keyframes: {
        shimmer: { "0%": { opacity: "0.5" }, "50%": { opacity: "1" }, "100%": { opacity: "0.5" } },
        "slide-in": { from: { transform: "translateY(8px)", opacity: "0" }, to: { transform: "none", opacity: "1" } },
        arrive: { from: { transform: "translateY(-6px)", boxShadow: "0 0 0 3px rgb(var(--accent) / 0.35)" }, to: { transform: "none", boxShadow: "none" } },
        bump: { "40%": { transform: "scale(1.08)" } },
      },
      animation: {
        shimmer: "shimmer 1.6s ease-in-out infinite",
        "slide-in": "slide-in 160ms ease-out",
        arrive: "arrive 600ms ease-out",
        bump: "bump 500ms ease-out",
      },
    },
  },
  plugins: [],
};
