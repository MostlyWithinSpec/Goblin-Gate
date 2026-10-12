// Pure helpers for the security profile pages (DNS filter, web filter, app
// control). Mirrors internal/config/validate.go; the server stays
// authoritative.
import type { Action3, App } from '../../api/objects'

const LABEL = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/

// validDomain: a hostname with at least one dot; "*." prefix and a trailing
// dot are allowed (both mean the same as the bare name).
export function validDomain(d: string): boolean {
  const h = d.replace(/\.$/, '').replace(/^\*\./, '')
  return h.length <= 253 && h.includes('.') && h.split('.').every((l) => LABEL.test(l))
}

export function validateDomainLists(p: { block_domains?: string[]; allow_domains?: string[] }): Record<string, string> {
  const e: Record<string, string> = {}
  for (const key of ['block_domains', 'allow_domains'] as const) {
    const bad = (p[key] ?? []).filter((d) => !validDomain(d))
    if (bad.length) e[key] = `Not a domain: ${bad.join(', ')}`
  }
  return e
}

// groupApps orders the catalog by group, then app name.
export function groupApps(apps: App[]): [string, App[]][] {
  const m = new Map<string, App[]>()
  for (const a of apps) m.set(a.group, [...(m.get(a.group) ?? []), a])
  return [...m.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([g, xs]) => [g, xs.sort((a, b) => a.name.localeCompare(b.name))])
}

// appAction: what a profile does with an app: its own override, else its
// group's action, else allow (internal/appctrl).
export function appAction(p: { groups?: Record<string, Action3>; apps?: Record<string, Action3> }, app: App): Action3 {
  return p.apps?.[app.id] ?? p.groups?.[app.group] ?? 'allow'
}

// setOverride sets or clears ("" = as group) an app override, keeping the
// maps free of empty entries.
export function setOverride<T extends { apps?: Record<string, Action3> }>(p: T, id: string, action: Action3 | ''): T {
  const apps = { ...(p.apps ?? {}) }
  if (action === '') delete apps[id]
  else apps[id] = action
  return { ...p, apps }
}

export function setGroup<T extends { groups?: Record<string, Action3> }>(p: T, group: string, action: Action3): T {
  const groups = { ...(p.groups ?? {}) }
  if (action === 'allow') delete groups[group]
  else groups[group] = action
  return { ...p, groups }
}

// summarize counts the non-allow decisions of a map ("3 block, 1 monitor").
export function summarize(m?: Record<string, string>): string {
  const n: Record<string, number> = {}
  for (const a of Object.values(m ?? {})) if (a !== 'allow') n[a] = (n[a] ?? 0) + 1
  return Object.entries(n).map(([a, c]) => `${c} ${a}`).join(', ')
}

// --- IPS (task 032) ---

// Common ET Open categories (the word after "ET" in a rule's msg); the
// default set is every category but POLICY, INFO, GAMES, CHAT and HUNTING.
export const ET_CATEGORIES = ['ATTACK_RESPONSE', 'CURRENT_EVENTS', 'DNS', 'DOS', 'EXPLOIT', 'EXPLOIT_KIT', 'MALWARE', 'MOBILE_MALWARE', 'PHISHING', 'SCAN', 'SHELLCODE', 'TROJAN', 'WEB_CLIENT', 'WEB_SERVER', 'WEB_SPECIFIC_APPS', 'WORM', 'POLICY', 'INFO', 'HUNTING', 'GAMES', 'CHAT']
export const ET_DEFAULT_EXCLUDED = ['POLICY', 'INFO', 'GAMES', 'CHAT', 'HUNTING']

// parseSignature splits a logged IPS signature "2100498: GPL ..." into its
// rule id and message (internal/logs/parser.go writes it that way).
export function parseSignature(sig?: string): { sid: number | null; msg: string } {
  const m = /^(\d+):\s*(.*)$/.exec(sig ?? '')
  return m ? { sid: Number(m[1]), msg: m[2] } : { sid: null, msg: sig ?? '' }
}

export interface Suppress {
  sid: number
  track?: string // '' | by_src | by_dst
  ip?: string
}

// addSuppress adds an exception unless the profile already has the same one.
export function addSuppress<T extends { suppress?: Suppress[] }>(p: T, s: Suppress): T {
  const list = p.suppress ?? []
  const same = (x: Suppress) => x.sid === s.sid && (x.track ?? '') === (s.track ?? '') && (x.ip ?? '') === (s.ip ?? '')
  return list.some(same) ? p : { ...p, suppress: [...list, s] }
}

export function validateIPS(p: { block_severity?: number; categories?: string[]; suppress?: Suppress[] }): Record<string, string> {
  const e: Record<string, string> = {}
  if (p.block_severity !== undefined && ![1, 2, 3].includes(p.block_severity)) e.block_severity = 'Severity must be 1, 2 or 3'
  const bad = (p.categories ?? []).filter((c) => !/^[A-Z0-9_]{1,32}$/.test(c))
  if (bad.length) e.categories = `Not a category: ${bad.join(', ')} (use names like MALWARE)`
  for (const s of p.suppress ?? []) {
    if (!Number.isInteger(s.sid) || s.sid <= 0) e.suppress = 'Each exception needs a rule id (SID)'
    else if (s.track && !/^\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?$/.test(s.ip ?? '')) e.suppress = `Exception for ${s.sid} needs an IPv4 address`
  }
  return e
}

// groupBySignature counts events per signature, most frequent first.
export function groupBySignature<E extends { signature?: string; severity?: string; time: number }>(events: E[]): { signature: string; severity?: string; count: number; last: number }[] {
  const m = new Map<string, { signature: string; severity?: string; count: number; last: number }>()
  for (const e of events) {
    const k = e.signature ?? ''
    const g = m.get(k) ?? { signature: k, severity: e.severity, count: 0, last: 0 }
    g.count++
    g.last = Math.max(g.last, e.time)
    m.set(k, g)
  }
  return [...m.values()].sort((a, b) => b.count - a.count)
}
