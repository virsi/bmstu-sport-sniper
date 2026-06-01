import type { Config } from 'tailwindcss'

/**
 * Tailwind-конфиг.
 *
 * Цветовая палитра расширена под бренд fizcultor: основной accent — синий BMSTU,
 * остальные цвета берутся из дефолтных. Слой `darkMode: 'class'` оставлен на будущее
 * (V2 — добавим тогл темы).
 */
const config: Config = {
  content: ['./index.html', './src/**/*.{vue,ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#eef5ff',
          100: '#d9e9ff',
          200: '#bcd6ff',
          300: '#8ebcff',
          400: '#5993ff',
          500: '#336bf5',
          600: '#214cd0',
          700: '#1b3da6',
          800: '#1a3585',
          900: '#1c326c',
        },
      },
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
    },
  },
  plugins: [],
}

export default config
