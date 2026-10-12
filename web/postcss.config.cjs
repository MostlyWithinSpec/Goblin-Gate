// Runs Tailwind over src/index.css (without this file Vite copies the
// @tailwind directives through and the UI ships unstyled).
module.exports = {
  plugins: {
    tailwindcss: {},
  },
}
