/** @type {import('tailwindcss').Config} */
export default {
  content: ["./src/**/*.{astro,html,js,jsx,md,mdx,svelte,ts,tsx,vue}"],
  darkMode: "class",
  theme: {
    extend: {
      fontFamily: {
        mono: ["JetBrains Mono", "Fira Code", "Cascadia Code", "monospace"],
        sans: ["Inter", "system-ui", "sans-serif"],
      },
      colors: {
        surface: {
          DEFAULT: "var(--bg)",
          secondary: "var(--bg-secondary)",
          card: "var(--bg-card)",
          inset: "var(--bg-inset)",
        },
        edge: {
          DEFAULT: "var(--border)",
          hover: "var(--border-hover)",
          subtle: "var(--border-subtle)",
        },
        content: {
          DEFAULT: "var(--text)",
          heading: "var(--text-heading)",
          muted: "var(--text-muted)",
          dim: "var(--text-dim)",
          inverse: "var(--text-inverse)",
        },
        accent: {
          green: "var(--green)",
          blue: "var(--blue)",
          orange: "var(--orange)",
          purple: "var(--purple)",
          yellow: "var(--yellow)",
        },
      },
      animation: {
        "scroll-left": "scrollLeft 30s linear infinite",
        "fade-in": "fadeIn 0.5s ease-in-out",
        "slide-up": "slideUp 0.5s ease-out",
      },
      keyframes: {
        scrollLeft: {
          "0%": { transform: "translateX(0)" },
          "100%": { transform: "translateX(-50%)" },
        },
        fadeIn: {
          "0%": { opacity: "0" },
          "100%": { opacity: "1" },
        },
        slideUp: {
          "0%": { transform: "translateY(20px)", opacity: "0" },
          "100%": { transform: "translateY(0)", opacity: "1" },
        },
      },
    },
  },
  plugins: [require("@tailwindcss/typography")],
};
