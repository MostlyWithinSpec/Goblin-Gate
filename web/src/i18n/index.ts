/// <reference types="vite/client" />
// Translations. Every visible string goes through t(key, vars): keys are
// flat ("<area>.<what>"), English in locales/en.json is the source catalog,
// other languages are locales/<code>.json with the same keys (translated on
// Weblate; a key a language lacks falls back to English).
//
//   t('commit.pending', { count: 3 })  -> "3 changes"   (keys commit.pending_one / _other)
//   t('menu.signedInAs', { user })     -> "Signed in as admin"
//
// Placeholders are {name}. A count picks the plural form the language uses
// (Intl.PluralRules): "<key>_one", "<key>_other", and for other languages also
// _zero, _two, _few, _many. Keys must be string literals, so the catalog check
// (src/__tests__/i18n.test.ts) can find every one.
import { useSyncExternalStore } from 'react'
import en from './locales/en.json'

export type Catalog = Record<string, string>
type Vars = Record<string, string | number>

const loaders: Record<string, () => Promise<Catalog>> = {}
for (const [path, load] of Object.entries(import.meta.glob<{ default: Catalog }>('./locales/*.json'))) {
  const code = path.slice('./locales/'.length, -'.json'.length)
  if (code !== 'en') loaders[code] = () => load().then((m) => m.default)
}

const english: Catalog = en
const englishPlural = new Intl.PluralRules('en')
let lang = 'en'
let catalog: Catalog = english
let plural = englishPlural
const listeners = new Set<() => void>()

const storageKey = 'goblin.language'

// languages lists the available languages, English first, each named in its
// own language ("Deutsch", "Français").
export function languages(): { code: string; name: string }[] {
  const codes = ['en', ...Object.keys(loaders).sort()]
  return codes.map((code) => {
    let name = code
    try {
      name = new Intl.DisplayNames([code], { type: 'language' }).of(code) ?? code
    } catch {
      /* unknown code: show it as is */
    }
    return { code, name: name.charAt(0).toLocaleUpperCase(code) + name.slice(1) }
  })
}

export function language(): string {
  return lang
}

// t returns the text for key in the current language.
export function t(key: string, vars?: Vars): string {
  let text: string | undefined
  if (vars && typeof vars.count === 'number') {
    const n = vars.count
    text = catalog[`${key}_${plural.select(n)}`] ?? catalog[`${key}_other`] ??
      english[`${key}_${englishPlural.select(n)}`] ?? english[`${key}_other`]
  }
  text ??= catalog[key] ?? english[key] ?? key
  if (!vars) return text
  return text.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m))
}

// setLanguage loads a language (unknown codes fall back to English) and
// re-renders everything that reads useLanguage.
export async function setLanguage(code: string): Promise<void> {
  const next = code in loaders ? code : 'en'
  const cat = next === 'en' ? english : await loaders[next]()
  lang = next
  catalog = cat
  plural = new Intl.PluralRules(next)
  document.documentElement.lang = next
  try {
    localStorage.setItem(storageKey, next)
  } catch {
    /* storage blocked: the choice lasts until reload */
  }
  listeners.forEach((l) => l())
}

// initialLanguage is the last language used in this browser, else the first
// of the browser's languages we have, else English.
export function initialLanguage(): string {
  try {
    const saved = localStorage.getItem(storageKey)
    if (saved && (saved === 'en' || saved in loaders)) return saved
  } catch {
    /* storage blocked */
  }
  for (const l of navigator.languages ?? [navigator.language]) {
    if (!l) continue
    const base = l.toLowerCase().split('-')[0]
    if (l in loaders) return l
    if (base === 'en' || base in loaders) return base
  }
  return 'en'
}

// useLanguage re-renders a component when the language changes. App keys its
// tree with it, so every t() call is run again.
export function useLanguage(): string {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => lang,
  )
}

// registerLanguage adds a catalog at run time (tests).
export function registerLanguage(code: string, cat: Catalog): void {
  loaders[code] = () => Promise.resolve(cat)
}
