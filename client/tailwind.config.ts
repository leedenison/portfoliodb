import type { Config } from 'tailwindcss';

const config: Config = {
  content: [
    './app/**/*.{js,ts,jsx,tsx,mdx}',
    './components/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      fontFamily: {
        display: ['var(--font-display)', 'sans-serif'],
        body: ['var(--font-body)', 'sans-serif'],
        mono: ['var(--font-mono)', 'monospace'],
      },
      colors: {
        primary: {
          DEFAULT: "#3E6B8A",
          dark: "#1B3A4B",
          light: "#BFD3E1",
        },
        accent: {
          DEFAULT: "#E07A2F",
          dark: "#B85E1D",
          soft: "#F7D8C1",
        },
        background: "#F4F6F9",
        surface: "#FFFFFF",
        border: "#D6DEE7",
        "text-primary": "#0F1720",
        "text-muted": "#5A6B7A",
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0', transform: 'translateY(8px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        'fade-in-fast': {
          from: { opacity: '0' },
          to: { opacity: '1' },
        },
        'slide-in-left': {
          from: { opacity: '0', transform: 'translateX(-12px)' },
          to: { opacity: '1', transform: 'translateX(0)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.4s ease-out both',
        'fade-in-fast': 'fade-in-fast 0.25s ease-out both',
        'fade-in-d1': 'fade-in 0.4s ease-out 0.05s both',
        'fade-in-d2': 'fade-in 0.4s ease-out 0.1s both',
        'fade-in-d3': 'fade-in 0.4s ease-out 0.15s both',
        'fade-in-d4': 'fade-in 0.4s ease-out 0.2s both',
        'slide-in-left': 'slide-in-left 0.3s ease-out both',
      },
    },
  },
  plugins: [],
};
export default config;
