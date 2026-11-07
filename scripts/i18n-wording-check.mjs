#!/usr/bin/env node
// Wording check for translation work: moving a page's text into the catalog
// must not change the English. Every entry of web/src/i18n/locales/en.json
// that is new or changed since <base> (default HEAD, i.e. the uncommitted
// changes) must consist of text the changed web sources had at <base>.
// Placeholders ({name}) may stand for anything; the text around them must be
// found as it was. Plural forms (_one, _other, ...) only need their words to
// be found (a singular may come from its plural), since the old code built
// them from pieces. Text a change only adds (a new page, a new tab: t() calls
// in diff hunks without removed lines) is new wording and is not checked.
//
//   node scripts/i18n-wording-check.mjs [base]
//
// Exit 1 lists the entries whose English is not on the old pages. A clean
// tree (nothing changed) passes.
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

const base = process.argv[2] || 'HEAD'
const catalog = 'web/src/i18n/locales/en.json'
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 << 20, stdio: ['ignore', 'pipe', 'ignore'] })

let before = {}
try {
  before = JSON.parse(git('show', `${base}:${catalog}`))
} catch {
  before = {} // the catalog is new
}
const now = JSON.parse(readFileSync(catalog, 'utf8'))

// New text is not moved text: a key whose every t('key') in the changed
// sources sits in a hunk that only adds lines (a new page, a new tab entry)
// has no old English to keep. A key on a replaced line is moved text and is
// checked, so rewording during translation is still caught.
const added = new Set()
const replaced = new Set()
{
  let pure = false
  const diff = git('diff', '-U0', base, '--', 'web/src', `:(exclude)${catalog}`)
  for (const line of diff.split('\n')) {
    const h = line.match(/^@@ -\d+(?:,(\d+))? \+/)
    if (h) {
      pure = h[1] === '0'
      continue
    }
    if (!line.startsWith('+') || line.startsWith('+++')) continue
    for (const m of line.matchAll(/\bt\(\s*['"`]([^'"`]+)['"`]/g)) (pure ? added : replaced).add(m[1])
  }
}
const isNewText = (key) => {
  const k = key.replace(/_(zero|one|two|few|many|other)$/, '')
  return (added.has(key) || added.has(k)) && !replaced.has(key) && !replaced.has(k)
}

const changed = git('diff', '--name-only', base, '--', 'web/src', `:(exclude)${catalog}`).split('\n').filter(Boolean)
const old = changed
  .map((f) => {
    try {
      return git('show', `${base}:${f}`)
    } catch {
      return '' // a new file
    }
  })
  .join('\n')
const unescape = (s) =>
  s
    .replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&apos;|&#39;/g, "'")
    .replace(/\\(['"`\\])/g, '$1')
const removed = unescape(old)
const removedWords = new Set(removed.match(/[\p{L}\p{N}']+/gu) ?? [])

const plural = /_(zero|one|two|few|many|other)$/
const bad = []
for (const [key, value] of Object.entries(now)) {
  if (before[key] === value) continue
  if (!(key in before) && isNewText(key)) continue
  if (plural.test(key)) {
    const words = value.replace(/\{\w+\}/g, ' ').match(/[\p{L}\p{N}']+/gu) ?? []
    const missing = words.filter((w) => !removedWords.has(w) && !removedWords.has(w + 's') && !removedWords.has(w + 'es'))
    if (missing.length) bad.push(`${key} = ${JSON.stringify(value)} (not on the old pages: ${missing.join(', ')})`)
    continue
  }
  const pieces = value.split(/\{\w+\}/).map((p) => p.trim()).filter((p) => p.length > 0)
  const missing = pieces.filter((p) => !removed.includes(p))
  if (missing.length) {
    const was = key in before ? ` (was ${JSON.stringify(before[key])})` : ''
    bad.push(`${key} = ${JSON.stringify(value)}${was}: not on the old pages: ${missing.map((m) => JSON.stringify(m)).join(', ')}`)
  }
}

if (bad.length) {
  console.error(`i18n wording check: ${bad.length} catalog entr${bad.length === 1 ? 'y has' : 'ies have'} English that the pages did not show before (since ${base}).`)
  console.error('Moving text into the catalog must keep the exact English, including punctuation and colons.')
  console.error('Copy the text from the removed code; put markup like <code> outside the t() call.')
  for (const b of bad) console.error('  ' + b)
  process.exit(1)
}
console.log(`i18n wording check: ok (${Object.keys(now).filter((k) => before[k] !== now[k]).length} new or changed entries since ${base})`)
