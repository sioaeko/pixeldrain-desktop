import type { Config } from "tailwindcss";

const v = (name: string) => `rgb(var(--${name}) / <alpha-value>)`;
const tokens = ["bg", "panel", "raised", "input", "inputhover", "line", "ink", "mute", "faint", "hl", "hltext", "link", "danger", "info", "ok", "warn", "shadow"];

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: Object.fromEntries(tokens.map((t) => [t, v(t)])),
      fontFamily: {
        // pixeldrain uses the system UI font; on Korean Windows that is Segoe UI with Malgun Gothic.
        sans: ["system-ui", '"Segoe UI"', '"Malgun Gothic"', "sans-serif"],
      },
      fontSize: {
        xs: ["0.75rem", { lineHeight: "1rem" }],
        sm: ["0.8125rem", { lineHeight: "1.25rem" }],
        base: ["0.875rem", { lineHeight: "1.375rem" }],
        md: ["1rem", { lineHeight: "1.5rem" }],
        lg: ["1.25rem", { lineHeight: "1.75rem" }],
        xl: ["1.75rem", { lineHeight: "2.125rem" }],
      },
    },
  },
  plugins: [],
} satisfies Config;
