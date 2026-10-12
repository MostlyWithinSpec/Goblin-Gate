import type { Config } from 'tailwindcss'

// Goblin Gate theme, after the Network Goblin Labs site (labs/assets/site.css):
// the gray scale runs from near-white to the site's blue-black panels (so every
// existing gray/dark: class follows the theme), blue is the site's cyan accent,
// green its goblin green. Dark is the default (src/theme.ts).
const config: Config = {
  darkMode: 'class', // src/theme.ts toggles the dark class on <html>
  content: [
    './src/**/*.{js,ts,jsx,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        gray: {
          50: '#f3f8fa',
          100: '#e4f0f4', // site --text
          200: '#cddde5',
          300: '#a9c0cc',
          400: '#8ea7b6', // site --dim
          500: '#5a7383', // site --faint
          600: '#33505f',
          700: '#1d3448', // site --edge
          800: '#0e1c29', // site --panel
          900: '#060c13', // site --bg
          950: '#03070b',
        },
        blue: {
          50: '#e9fdfb',
          100: '#cbfaf5',
          200: '#9ff5ec',
          300: '#7ff3ea',
          400: '#3be8da', // site --cyan
          500: '#18c9bb',
          600: '#0c9389',
          700: '#09766e',
          800: '#085c56',
          900: '#063f3b',
        },
        green: {
          50: '#f2fce9',
          100: '#e0f8cc',
          200: '#c4f09f',
          300: '#a8e976',
          400: '#8ee34f', // site --green
          500: '#6fc632',
          600: '#559c25',
          700: '#42791e',
          800: '#355f1b',
          900: '#2c4f19',
        },
      },
      fontFamily: {
        sans: ['Rajdhani', 'system-ui', 'sans-serif'],
        display: ['Orbitron', 'Rajdhani', 'sans-serif'],
      },
    },
  },
  plugins: [],
}
export default config
