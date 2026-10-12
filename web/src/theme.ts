// Light/dark theme. Dark (the Network Goblin Labs look) is the default; the
// choice is remembered per browser. main.tsx applies it before the first render
// so no page (login and setup included) flashes the other theme.
export type Theme = 'dark' | 'light'

export function savedTheme(): Theme {
  try {
    return localStorage.getItem('theme') === 'light' ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

export function applyTheme(t: Theme) {
  document.documentElement.classList.toggle('dark', t === 'dark')
}

export function toggleTheme(): Theme {
  const next: Theme = document.documentElement.classList.contains('dark') ? 'light' : 'dark'
  applyTheme(next)
  try {
    localStorage.setItem('theme', next)
  } catch {
    // private mode: the choice lasts for this page only
  }
  return next
}
