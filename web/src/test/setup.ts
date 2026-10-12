import '@testing-library/jest-dom/vitest'

// React Router v6 prints "Future Flag Warning" notices about v7 changes on
// every router render. They are expected and harmless; keep them out of the
// test output so real warnings stand out.
const warn = console.warn
console.warn = (...args: unknown[]) => {
  if (typeof args[0] === 'string' && args[0].includes('React Router Future Flag Warning')) return
  warn(...args)
}
