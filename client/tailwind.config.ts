import type { Config } from 'tailwindcss';

const config: Config = {
  content: [
    './app/**/*.{js,ts,jsx,tsx,mdx}',
    './components/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        primary: {
          DEFAULT: "#3E6B8A",
          dark: "#27465D",
          light: "#BFD3E1",
        },
        accent: {
          DEFAULT: "#E07A2F",
          dark: "#B85E1D",
          soft: "#F7D8C1",
        },
        background: "#F6F8FB",
        surface: "#FFFFFF",
        border: "#D6DEE7",
        "text-primary": "#0F1720",
        "text-muted": "#5A6B7A",
      },
    },
  },
  plugins: [],
};
export default config;
