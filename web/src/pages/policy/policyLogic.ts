// Pure helpers for the policy page: validation, ids, list edits and grouping.
import type { Policy, PolicyObjects } from '../../api/policy'

export type FieldErrors = Partial<Record<keyof Policy, string>>

// validatePolicy mirrors the server's checks (internal/config/validate_policy.go)
// closely enough to catch mistakes before saving; the server stays authoritative.
// others is every other policy (without the one being edited).
export function validatePolicy(p: Policy, objects: PolicyObjects, others: Policy[]): FieldErrors {
  const e: FieldErrors = {}
  if (!p.name.trim()) e.name = 'Name is required'
  else if (others.some((o) => o.name === p.name)) e.name = 'Name is already used'
  if (!Number.isInteger(p.id) || p.id < 1) e.id = 'ID must be a positive number'
  else if (others.some((o) => o.id === p.id)) e.id = 'ID is already used'

  const need = (field: 'src_intf' | 'dst_intf' | 'src_addr' | 'dst_addr' | 'service', label: string, known: string[]) => {
    const v = p[field] ?? []
    if (v.length === 0) e[field] = `Pick at least one ${label}`
    else {
      const unknown = v.filter((x) => !known.includes(x))
      if (unknown.length) e[field] = `Unknown ${label}: ${unknown.join(', ')}`
    }
  }
  need('src_intf', 'incoming interface', objects.interfaces)
  need('dst_intf', 'outgoing interface', objects.interfaces)
  need('src_addr', 'source', objects.addresses)
  need('dst_addr', 'destination', objects.addresses)
  need('service', 'service', objects.services)
  if (p.action !== 'accept' && p.action !== 'deny') e.action = 'Action must be accept or deny'
  return e
}

export const nextId = (ps: Policy[]) => ps.reduce((m, p) => Math.max(m, p.id), 0) + 1

export function newPolicy(ps: Policy[]): Policy {
  return {
    id: nextId(ps),
    name: '',
    enabled: true,
    src_intf: [],
    dst_intf: [],
    src_addr: ['all'],
    dst_addr: ['all'],
    service: ['any'],
    action: 'accept',
    nat: false,
    log: 'off',
  }
}

export function clonePolicy(p: Policy, ps: Policy[]): Policy {
  let name = `${p.name}-copy`
  for (let i = 2; ps.some((o) => o.name === name); i++) name = `${p.name}-copy${i}`
  const copy = JSON.parse(JSON.stringify(p)) as Policy // deep copy of the lists
  return { ...copy, id: nextId(ps), name }
}

// insertAt returns ps with p inserted at index i.
export const insertAt = (ps: Policy[], i: number, p: Policy) => [...ps.slice(0, i), p, ...ps.slice(i)]

// move returns ps with the item at from moved to index to.
export function move(ps: Policy[], from: number, to: number): Policy[] {
  const out = [...ps]
  const [x] = out.splice(from, 1)
  out.splice(to, 0, x)
  return out
}

// replace returns ps with the policy whose id is id replaced by p (or p appended).
export function replace(ps: Policy[], id: number, p: Policy): Policy[] {
  const i = ps.findIndex((o) => o.id === id)
  return i < 0 ? [...ps, p] : ps.map((o, j) => (j === i ? p : o))
}

export interface PairGroup {
  key: string // "lan → wan1"
  policies: Policy[]
}

// groupByPair groups policies by interface pair, keeping policy order inside
// each group and ordering groups by their first policy.
export function groupByPair(ps: Policy[]): PairGroup[] {
  const groups: PairGroup[] = []
  const byKey = new Map<string, PairGroup>()
  for (const p of ps) {
    const key = `${(p.src_intf ?? []).join(', ') || '—'} → ${(p.dst_intf ?? []).join(', ') || '—'}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, policies: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    g.policies.push(p)
  }
  return groups
}

export type SecurityLevel = 'off' | 'basic' | 'strict'

// applySecurityLevel fills the profile fields with profiles named like the
// level ("basic"/"strict") when they exist; "off" clears them.
export function applySecurityLevel(p: Policy, level: SecurityLevel, objects: PolicyObjects): Policy {
  const pick = (list: string[]) => (level !== 'off' && list.includes(level) ? level : undefined)
  return {
    ...p,
    ips_profile: pick(objects.profiles.ips),
    webfilter_profile: pick(objects.profiles.webfilter),
    dnsfilter_profile: pick(objects.profiles.dnsfilter),
    appctrl_profile: pick(objects.profiles.appctrl),
  }
}

// profileBadges lists the security features a policy uses, for the table.
export function profileBadges(p: Policy): string[] {
  const b: string[] = []
  if (p.ips_profile) b.push('IPS')
  if (p.webfilter_profile) b.push('WEB')
  if (p.dnsfilter_profile) b.push('DNS')
  if (p.appctrl_profile) b.push('APP')
  if (p.threat_feeds?.length) b.push('FEED')
  return b
}

// clean drops empty optional fields so the saved JSON matches what the
// server would marshal (omitempty).
export function clean(p: Policy): Policy {
  const out: Policy = { ...p }
  for (const k of Object.keys(out) as (keyof Policy)[]) {
    const v = out[k]
    if (v === undefined || v === '' || (Array.isArray(v) && v.length === 0)) delete out[k]
  }
  if (out.nat === false) delete out.nat
  return out
}
