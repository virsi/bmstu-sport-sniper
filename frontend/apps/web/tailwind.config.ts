import type { Config } from 'tailwindcss'

/**
 * Tailwind-конфиг.
 *
 * Премиум-палитра в стиле Linear / Vercel / Cal.com:
 * - Темная база (zinc-950 / zinc-900) + светлая поверхность (zinc-100/white).
 * - Primary brand — насыщенный emerald (символика «спорт/здоровье»).
 * - Accent — violet для алёртов / live-индикаторов.
 * - Surface-уровни — для glassmorphism (полупрозрачные карточки на blur).
 *
 * `darkMode: 'class'` — активируется toggle-ом на <html>; по умолчанию dark.
 */
const config: Config = {
  content: ['./index.html', './src/**/*.{vue,ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // Primary brand: emerald, evokes sport/vitality. Tuned for dark BG.
        brand: {
          50: '#ecfdf5',
          100: '#d1fae5',
          200: '#a7f3d0',
          300: '#6ee7b7',
          400: '#34d399',
          500: '#10b981',
          600: '#059669',
          700: '#047857',
          800: '#065f46',
          900: '#064e3b',
          950: '#022c22',
        },
        // Accent violet — live-индикаторы, NEW-бейджи, sparing usage.
        accent: {
          50: '#f5f3ff',
          100: '#ede9fe',
          200: '#ddd6fe',
          300: '#c4b5fd',
          400: '#a78bfa',
          500: '#8b5cf6',
          600: '#7c3aed',
          700: '#6d28d9',
          800: '#5b21b6',
          900: '#4c1d95',
        },
        // Surface tokens for glass cards on dark.
        surface: {
          0: 'rgba(255,255,255,0.02)',
          1: 'rgba(255,255,255,0.04)',
          2: 'rgba(255,255,255,0.06)',
          3: 'rgba(255,255,255,0.08)',
        },
      },
      fontFamily: {
        sans: [
          'Inter var',
          'Inter',
          'ui-sans-serif',
          'system-ui',
          '-apple-system',
          'sans-serif',
        ],
        mono: [
          'JetBrains Mono',
          'ui-monospace',
          'SFMono-Regular',
          'Menlo',
          'Monaco',
          'monospace',
        ],
      },
      fontSize: {
        // Modular type scale (1.25 ratio).
        '2xs': ['0.6875rem', { lineHeight: '1rem' }],
        display: ['3.5rem', { lineHeight: '1.05', letterSpacing: '-0.03em' }],
      },
      borderRadius: {
        '4xl': '2rem',
      },
      boxShadow: {
        glow: '0 0 0 1px rgba(16,185,129,0.20), 0 8px 32px -8px rgba(16,185,129,0.35)',
        'glow-violet':
          '0 0 0 1px rgba(139,92,246,0.20), 0 8px 32px -8px rgba(139,92,246,0.35)',
        elevated:
          '0 1px 2px rgba(0,0,0,0.04), 0 10px 30px -12px rgba(0,0,0,0.20)',
        'inner-light': 'inset 0 1px 0 0 rgba(255,255,255,0.05)',
      },
      backgroundImage: {
        'grid-zinc':
          "url(\"data:image/svg+xml,%3csvg xmlns='http://www.w3.org/2000/svg' width='40' height='40' fill='none'%3e%3cpath d='M0 .5H40M.5 0V40' stroke='rgba(255,255,255,0.04)'/%3e%3c/svg%3e\")",
        'gradient-hero':
          'radial-gradient(120% 80% at 50% 0%, rgba(16,185,129,0.25) 0%, rgba(139,92,246,0.18) 35%, rgba(0,0,0,0) 70%)',
        'gradient-brand':
          'linear-gradient(135deg, #10b981 0%, #059669 50%, #047857 100%)',
        'gradient-accent':
          'linear-gradient(135deg, #a78bfa 0%, #8b5cf6 50%, #6d28d9 100%)',
      },
      keyframes: {
        'pulse-glow': {
          '0%, 100%': { opacity: '1', boxShadow: '0 0 0 0 rgba(16,185,129,0.7)' },
          '50%': { opacity: '0.8', boxShadow: '0 0 0 8px rgba(16,185,129,0)' },
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
        'slide-in-right': {
          '0%': { transform: 'translateX(110%)', opacity: '0' },
          '100%': { transform: 'translateX(0)', opacity: '1' },
        },
        'fade-up': {
          '0%': { transform: 'translateY(8px)', opacity: '0' },
          '100%': { transform: 'translateY(0)', opacity: '1' },
        },
      },
      animation: {
        'pulse-glow': 'pulse-glow 2s ease-in-out infinite',
        shimmer: 'shimmer 2.2s linear infinite',
        'slide-in-right': 'slide-in-right 280ms cubic-bezier(0.16, 1, 0.3, 1)',
        'fade-up': 'fade-up 320ms cubic-bezier(0.16, 1, 0.3, 1)',
      },
    },
  },
  plugins: [],
}

export default config
