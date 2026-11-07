import { afterEach, describe, expect, test, vi } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/auth', async (orig) => {
  const real = await orig<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session' }) }
})
vi.mock('../api/prefs', () => ({ getPrefs: vi.fn(), savePrefs: vi.fn() }))
import * as prefs from '../api/prefs'
import { language, languages, registerLanguage, setLanguage, t } from '../i18n'
import en from '../i18n/locales/en.json'
import UserMenu from '../components/UserMenu'

const src = join(__dirname, '..')
const english = en as Record<string, string>

// every .ts/.tsx file of the app (not the tests, nor i18n itself: its comments show examples)
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return ['__tests__', 'test', 'i18n'].includes(name) ? [] : sources(p)
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : []
  })
}

const pluralForms = /_(zero|one|two|few|many|other)$/
const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()

afterEach(async () => {
  await setLanguage('en')
})

describe('t()', () => {
  test('placeholders, plurals and the key itself as the last resort', () => {
    registerLanguage('xx', {})
    expect(t('commit.committed', { revision: 7 })).toBe('Committed revision 7')
    expect(t('no.such.key')).toBe('no.such.key')
    expect(t('commit.committed', {})).toBe('Committed revision {revision}')
  })

  test('another language, with English for what it lacks and its own plural rules', async () => {
    registerLanguage('fr', { 'login.signIn': 'Se connecter', 'x.items_one': '{count} élément', 'x.items_other': '{count} éléments' })
    await setLanguage('fr')
    expect(language()).toBe('fr')
    expect(document.documentElement.lang).toBe('fr')
    expect(t('login.signIn')).toBe('Se connecter')
    expect(t('login.password')).toBe('Password')
    // French uses "one" for 0 and 1
    expect(t('x.items', { count: 0 })).toBe('0 élément')
    expect(t('x.items', { count: 2 })).toBe('2 éléments')
    await setLanguage('nope')
    expect(language()).toBe('en')
  })
})

describe('the catalog', () => {
  const files = sources(src)
  const used = new Map<string, string>() // key -> first file using it
  for (const f of files) {
    const text = readFileSync(f, 'utf-8')
    for (const m of text.matchAll(/(?<![\w.$])t\(\s*(['"`])((?:(?!\1).)*)\1/g)) {
      if (!used.has(m[2])) used.set(m[2], relative(src, f))
    }
  }

  test('keys are plain string literals', () => {
    const bad: string[] = []
    for (const f of files) {
      const text = readFileSync(f, 'utf-8')
      for (const m of text.matchAll(/(?<![\w.$])t\(\s*(?!['"])([^)\s][^)]*)/g)) bad.push(`${relative(src, f)}: t(${m[1]}`)
      for (const m of text.matchAll(/(?<![\w.$])t\(\s*`/g)) bad.push(`${relative(src, f)}: template key at ${m.index}`)
    }
    expect(bad).toEqual([])
  })

  test('every key used is in en.json', () => {
    const missing = [...used].filter(([k]) => !(k in english) && !(`${k}_other` in english)).map(([k, f]) => `${k} (${f})`)
    expect(missing).toEqual([])
  })

  test('every key in en.json is used', () => {
    const unused = Object.keys(english).filter((k) => !used.has(k) && !used.has(k.replace(pluralForms, '')))
    expect(unused).toEqual([])
  })

  test('translations only have keys and placeholders English has', () => {
    const dir = join(src, 'i18n', 'locales')
    const problems: string[] = []
    for (const name of readdirSync(dir).filter((n) => n.endsWith('.json') && n !== 'en.json')) {
      const cat = JSON.parse(readFileSync(join(dir, name), 'utf-8')) as Record<string, string>
      for (const [k, v] of Object.entries(cat)) {
        const base = k.replace(pluralForms, '')
        const source = english[k] ?? english[`${base}_other`]
        if (source === undefined) problems.push(`${name}: ${k} is not in en.json`)
        else if (placeholders(v).join() !== placeholders(source).join() && !(base !== k && placeholders(v).every((p) => p === 'count')))
          problems.push(`${name}: ${k} has placeholders ${placeholders(v)}, English has ${placeholders(source)}`)
      }
    }
    expect(problems).toEqual([])
  })
})

describe('the language picker', () => {
  test('hidden while English is the only language', async () => {
    vi.mocked(prefs.getPrefs).mockResolvedValue({})
    if (languages().length > 1) return // a translation exists: covered by the next test
    render(<MemoryRouter><UserMenu /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: 'Account menu' }))
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  test('switches the language and stores it for the admin', async () => {
    registerLanguage('de', { 'menu.logOut': 'Abmelden', 'menu.account': 'Kontomenü' })
    vi.mocked(prefs.getPrefs).mockResolvedValue({ other: 1 } as never)
    vi.mocked(prefs.savePrefs).mockResolvedValue({ status: 'saved' })
    render(<MemoryRouter><UserMenu /></MemoryRouter>)
    fireEvent.click(await screen.findByRole('button', { name: 'Account menu' }))
    const select = screen.getByRole('combobox')
    expect(screen.getByRole('option', { name: 'Deutsch' })).toBeInTheDocument()
    fireEvent.change(select, { target: { value: 'de' } })
    await waitFor(() => expect(prefs.savePrefs).toHaveBeenCalledWith({ other: 1, language: 'de' }))
    expect(language()).toBe('de')
  })

  test("the admin's stored language is applied when the menu loads", async () => {
    registerLanguage('de', { 'menu.account': 'Kontomenü' })
    vi.mocked(prefs.getPrefs).mockResolvedValue({ language: 'de' })
    render(<MemoryRouter><UserMenu /></MemoryRouter>)
    await waitFor(() => expect(language()).toBe('de'))
  })
})
