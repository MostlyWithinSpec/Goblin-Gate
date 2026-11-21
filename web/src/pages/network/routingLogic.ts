// Pure logic behind Network → Routing: turning the stored `routing` section
// into a form and back, checking it the way the server does, and sorting out
// what the live FRR status means for the badges. Kept out of the component so
// the tests can drive it directly.
import type { Candidate } from '../../api/objects'
import type { BGPNeighbor, LearnedRoute, PrefixRule, RoutingConfig, RoutingStatus } from '../../api/routing'
import { t } from '../../i18n'

const MAX_UINT32 = 4294967295
// AS numbers the server refuses besides 0 and the maximum: 23456 is the
// documentation AS, 4294967295 is reserved.
const RESERVED_AS = [23456, MAX_UINT32]
// A neighbour description or password is at most 80 characters; a description
// may hold these characters only (config.frrTextRe), a password any printable
// character without spaces.
const TEXT_MAX = 80
const DESC_RE = /^[A-Za-z0-9 ._,:/()+@#-]*$/
const PASSWORD_RE = /^[\x21-\x7e]*$/
const ADDR_RE = /^[0-9.]+$/
const V6_RE = /^[0-9A-Fa-f:]+$/
const AREA_RE = /^(\d+|(\d{1,3}\.){3}\d{1,3})$/
const MAX_AREAS = 64
const MAX_NEIGHBORS = 64 // config.MaxBGPNeighbors
const MAX_RULES = 256 // config.MaxPrefixRules
const MAX_ANNOUNCES = 256

export const routingWikiURL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Dynamic-Routing'

// The form mirrors the config object one field at a time, so saving only drops
// what is a default. Numbers stay strings while typed and become numbers on the
// way out, which keeps a half-typed field from becoming 0.
export interface RuleForm {
  action: string
  prefix: string
  max_length: string
}
export interface NeighborForm {
  address: string
  remote_as: string
  enabled: boolean
  description: string
  password: string
  multihop: string
  import: RuleForm[]
  export: RuleForm[]
}
export interface AreaForm {
  id: string
  interfaces: { name: string; cost: string; passive: boolean }[]
}
export interface RoutingForm {
  router_id: string
  bgp_enabled: boolean
  bgp_as: string
  bgp_announce: string[]
  bgp_redistribute_connected: boolean
  bgp_redistribute_static: boolean
  bgp_neighbors: NeighborForm[]
  ospf_enabled: boolean
  ospf_areas: AreaForm[]
  ospf_redistribute_connected: boolean
  ospf_redistribute_static: boolean
}

export const emptyRule = (): RuleForm => ({ action: 'permit', prefix: '', max_length: '' })
export const emptyNeighbor = (): NeighborForm => ({
  address: '',
  remote_as: '',
  enabled: true,
  description: '',
  password: '',
  multihop: '',
  import: [],
  export: [],
})
export const emptyArea = (): AreaForm => ({ id: '0', interfaces: [] })

const num = (s: string | number | undefined): string => (s === undefined || s === 0 ? '' : String(s))

export function formOf(r: RoutingConfig | undefined): RoutingForm {
  const b = r?.bgp
  const o = r?.ospf
  return {
    router_id: r?.router_id ?? '',
    bgp_enabled: b?.enabled ?? false,
    bgp_as: num(b?.as),
    bgp_announce: b?.announce ?? [],
    bgp_redistribute_connected: b?.redistribute_connected ?? false,
    bgp_redistribute_static: b?.redistribute_static ?? false,
    bgp_neighbors: (b?.neighbors ?? []).map((n) => ({
      address: n.address ?? '',
      remote_as: num(n.remote_as),
      enabled: n.enabled,
      description: n.description ?? '',
      password: n.password ?? '',
      multihop: num(n.multihop),
      import: (n.import ?? []).map(ruleFormOf),
      export: (n.export ?? []).map(ruleFormOf),
    })),
    ospf_enabled: o?.enabled ?? false,
    ospf_areas: (o?.areas ?? []).map((a) => ({
      id: a.id ?? '',
      interfaces: (a.interfaces ?? []).map((i) => ({ name: i.name ?? '', cost: num(i.cost), passive: i.passive ?? false })),
    })),
    ospf_redistribute_connected: o?.redistribute_connected ?? false,
    ospf_redistribute_static: o?.redistribute_static ?? false,
  }
}

function ruleFormOf(r: PrefixRule): RuleForm {
  return { action: r.action || 'permit', prefix: r.prefix ?? '', max_length: num(r.max_length) }
}

// intNum parses a form field as a whole number; '' is no value.
function intNum(s: string): number | null {
  const raw = s.trim()
  if (raw === '') return null
  if (!/^-?\d+$/.test(raw)) return Number.NaN
  return Number(raw)
}

function ruleOf(f: RuleForm): PrefixRule {
  const out: PrefixRule = { action: f.action === 'deny' ? 'deny' : 'permit', prefix: f.prefix.trim() }
  const n = intNum(f.max_length)
  if (n !== null && Number.isFinite(n) && n !== 0) out.max_length = n
  return out
}

function neighborOf(f: NeighborForm): BGPNeighbor {
  const out: BGPNeighbor = { address: f.address.trim(), remote_as: asNumber(f.remote_as), enabled: f.enabled }
  const desc = f.description.trim()
  if (desc) out.description = desc
  // A blank password is left out so the server keeps the stored one; the field
  // says so under the input.
  const pw = f.password.trim()
  if (pw) out.password = pw
  const hop = intNum(f.multihop)
  if (hop !== null && Number.isFinite(hop) && hop !== 0) out.multihop = hop
  if (f.import.length > 0) out.import = f.import.map(ruleOf)
  if (f.export.length > 0) out.export = f.export.map(ruleOf)
  return out
}

function asNumber(s: string): number {
  const n = intNum(s)
  return n === null || !Number.isFinite(n) ? 0 : n
}

export function buildRouting(f: RoutingForm): RoutingConfig {
  const out: RoutingConfig = { bgp: { enabled: f.bgp_enabled, as: asNumber(f.bgp_as) }, ospf: { enabled: f.ospf_enabled } }
  const id = f.router_id.trim()
  if (id) out.router_id = id
  if (f.bgp_enabled) {
    if (f.bgp_announce.length) out.bgp.announce = f.bgp_announce
    if (f.bgp_redistribute_connected) out.bgp.redistribute_connected = true
    if (f.bgp_redistribute_static) out.bgp.redistribute_static = true
    if (f.bgp_neighbors.length) out.bgp.neighbors = f.bgp_neighbors.map(neighborOf)
  }
  if (f.ospf_enabled) {
    if (f.ospf_areas.length) {
      out.ospf.areas = f.ospf_areas.map((a) => ({
        id: a.id.trim(),
        interfaces: a.interfaces.map((i) => {
          const o: { name: string; cost?: number; passive?: boolean } = { name: i.name }
          const c = intNum(i.cost)
          if (c !== null && Number.isFinite(c) && c !== 0) o.cost = c
          if (i.passive) o.passive = true
          return o
        }),
      }))
    }
    if (f.ospf_redistribute_connected) out.ospf.redistribute_connected = true
    if (f.ospf_redistribute_static) out.ospf.redistribute_static = true
  }
  return out
}

// addrBits is the length of an address in bits, -1 when it is neither v4 nor v6.
function addrBits(a: string): number {
  if (ADDR_RE.test(a)) return a.split('.').length === 4 ? 32 : -1
  if (V6_RE.test(a) && a.includes(':')) return 128
  return -1
}

// prefixParts splits a CIDR into its address and prefix length; no "/" means a
// full-length address.
function prefixParts(p: string): { addr: string; bits: number; total: number } | null {
  const slash = p.indexOf('/')
  const addr = slash < 0 ? p : p.slice(0, slash)
  const total = addrBits(addr)
  if (total < 0) return null
  if (slash < 0) return { addr, bits: total, total }
  if (!/^\d+$/.test(p.slice(slash + 1))) return null
  return { addr, bits: Number(p.slice(slash + 1)), total }
}

function checkRules(list: RuleForm[], key: string, errs: Record<string, string>) {
  if (list.length > MAX_RULES) errs[key] = t('routing.tooManyRules', { max: MAX_RULES })
  list.forEach((r, i) => {
    if (r.action !== 'permit' && r.action !== 'deny') errs[`${key}[${i}].action`] = t('routing.ruleActionInvalid')
    const p = prefixParts(r.prefix.trim())
    if (!p) {
      errs[`${key}[${i}].prefix`] = t('routing.prefixInvalid')
      return
    }
    // host bits: the address must be the network itself, so an address of all
    // zero bits after the prefix is the network.
    if (!networkOnly(p.addr, p.bits, p.total)) errs[`${key}[${i}].prefix`] = t('routing.prefixHostBits')
    const m = intNum(r.max_length)
    if (m !== null && Number.isFinite(m) && m !== 0 && (m < p.bits || m > p.total))
      errs[`${key}[${i}].max_length`] = t('routing.maxLengthInvalid', { min: p.bits, max: p.total })
  })
}

// networkOnly says whether an address is the network address of a /bits prefix.
// Working on the bytes keeps it exact without BigInt maths.
function networkOnly(addr: string, bits: number, total: number): boolean {
  if (bits === total) return true
  const bytes = addr.includes(':') ? v6Bytes(addr) : v4Bytes(addr)
  if (!bytes) return false
  for (let i = bits; i < total; i++) {
    const byte = i >> 3
    const mask = 0x80 >> (i % 8)
    if ((bytes[byte] & mask) !== 0) return false
  }
  return true
}

function v4Bytes(a: string): number[] | null {
  const parts = a.split('.')
  if (parts.length !== 4) return null
  const out = parts.map(Number)
  return out.every((n) => Number.isInteger(n) && n >= 0 && n <= 255) ? out : null
}

// v6Bytes expands an IPv6 address (with "::") into 16 bytes; null when a group
// is out of range or the address is malformed.
function v6Bytes(a: string): number[] | null {
  const halves = a.split('::')
  if (halves.length > 2) return null
  const head = halves[0] ? halves[0].split(':') : []
  const tail = halves.length === 2 && halves[1] ? halves[1].split(':') : []
  const groups = halves.length === 2 ? [...head, ...Array(Math.max(0, 8 - head.length - tail.length)).fill('0'), ...tail] : [...head, ...tail]
  if (groups.length !== 8) return null
  const out: number[] = []
  for (const g of groups) {
    if (!/^[0-9A-Fa-f]{1,4}$/.test(g)) return null
    const n = parseInt(g, 16)
    out.push(n >> 8, n & 0xff)
  }
  return out
}

// validateRouting mirrors what the server checks (config.checkDynamicRouting)
// for everything a browser can decide. The keys are "routing." paths with the
// "routing." prefix dropped, so they match the server's messages too.
export function validateRouting(f: RoutingForm): Record<string, string> {
  const e: Record<string, string> = {}
  if (!f.bgp_enabled && !f.ospf_enabled) return e
  const id = f.router_id.trim()
  if (id && addrBits(id) !== 32) e['router_id'] = t('routing.routerIdInvalid')
  if (f.bgp_enabled) {
    const as = intNum(f.bgp_as)
    if (as === null || !Number.isFinite(as) || as < 1 || as > MAX_UINT32 || RESERVED_AS.includes(as))
      e['bgp.as'] = t('routing.asInvalid')
    if (f.bgp_announce.length > MAX_ANNOUNCES) e['bgp.announce'] = t('routing.tooManyAnnounce', { max: MAX_ANNOUNCES })
    if (f.bgp_neighbors.length > MAX_NEIGHBORS) e['bgp.neighbors'] = t('routing.tooManyNeighbors', { max: MAX_NEIGHBORS })
    const seen = new Set<string>()
    f.bgp_neighbors.forEach((n, i) => {
      const p = `bgp.neighbors[${i}]`
      const addr = n.address.trim()
      const bits = addrBits(addr)
      if (bits < 0) e[`${p}.address`] = t('routing.neighborAddressInvalid')
      else if (addr === '0.0.0.0' || isLoopback(addr) || isMulticast(addr))
        e[`${p}.address`] = t('routing.neighborAddressInvalid')
      else if (seen.has(addr)) e[`${p}.address`] = t('routing.neighborDuplicate')
      seen.add(addr)
      const as = intNum(n.remote_as)
      if (as === null || !Number.isFinite(as) || as < 1 || as > MAX_UINT32 || RESERVED_AS.includes(as))
        e[`${p}.remote_as`] = t('routing.neighborAsInvalid')
      if (n.description.length > TEXT_MAX || !DESC_RE.test(n.description)) e[`${p}.description`] = t('routing.descriptionInvalid')
      if (n.password.length > TEXT_MAX || !PASSWORD_RE.test(n.password)) e[`${p}.password`] = t('routing.passwordInvalid')
      const hop = intNum(n.multihop)
      if (hop !== null && (!Number.isFinite(hop) || hop < 0 || hop > 255)) e[`${p}.multihop`] = t('routing.multihopInvalid')
      checkRules(n.import, `${p}.import`, e)
      checkRules(n.export, `${p}.export`, e)
    })
  }
  if (f.ospf_enabled) {
    if (f.ospf_areas.length === 0) e['ospf.areas'] = t('routing.needsArea')
    if (f.ospf_areas.length > MAX_AREAS) e['ospf.areas'] = t('routing.tooManyAreas', { max: MAX_AREAS })
    const areaIDs = new Set<string>()
    const used = new Set<string>()
    f.ospf_areas.forEach((a, i) => {
      const p = `ospf.areas[${i}]`
      const id = a.id.trim()
      const dotted = areaID(id)
      if (dotted === null) e[`${p}.id`] = t('routing.areaIdInvalid')
      else if (areaIDs.has(dotted)) e[`${p}.id`] = t('routing.areaDuplicate', { id: dotted })
      areaIDs.add(dotted ?? id)
      if (a.interfaces.length === 0) e[`${p}.interfaces`] = t('routing.needsInterface')
      a.interfaces.forEach((x, j) => {
        const q = `${p}.interfaces[${j}]`
        if (!x.name) e[`${q}.name`] = t('routing.interfaceRequired')
        else if (used.has(x.name)) e[`${q}.name`] = t('routing.interfaceInTwoAreas', { name: x.name })
        used.add(x.name)
        const c = intNum(x.cost)
        if (c !== null && (!Number.isFinite(c) || c < 0 || c > 65535)) e[`${q}.cost`] = t('routing.costInvalid')
      })
    })
  }
  return e
}

function isMulticast(addr: string): boolean {
  return addr.startsWith('224.') || addr.startsWith('ff') || addr.startsWith('FF')
}

// isLoopback matches what Go calls a loopback address (netip.Addr.IsLoopback):
// any 127.0.0.0/8 for IPv4 and ::1 for IPv6.
function isLoopback(addr: string): boolean {
  return addr.startsWith('127.') || addr === '::1'
}

// areaID returns an OSPF area ID in dotted form, as the server does
// (config.OSPFAreaID): a plain number becomes its dotted equivalent, and a
// dotted ID must be a real IPv4 address. null when it is neither.
export function areaID(id: string): string | null {
  if (/^\d+$/.test(id)) {
    const n = Number(id)
    if (!Number.isSafeInteger(n) || n > 4294967295) return null
    return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join('.')
  }
  return AREA_RE.test(id) && v4Bytes(id) !== null ? id : null
}

// announceOptions offers both kinds of thing BGP may announce in one picker:
// address objects of type host or subnet, and interfaces and VLANs.
export function announceOptions(cfg: Candidate): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = []
  for (const a of cfg.addresses ?? []) {
    if (a.type === 'host' || a.type === 'subnet') out.push({ value: a.name, label: `${a.name} — ${a.value}` })
  }
  for (const i of cfg.interfaces ?? []) out.push({ value: i.name, label: `${i.name} — ${i.address || i.device}` })
  for (const v of cfg.vlans ?? []) out.push({ value: v.name, label: `${v.name} — ${v.address || v.parent}` })
  return out
}

// interfaceOptions lists what an OSPF area may run on.
export function interfaceOptions(cfg: Candidate): string[] {
  return [...(cfg.interfaces ?? []).map((i) => i.name), ...(cfg.vlans ?? []).map((v) => v.name)]
}

// moveRule swaps one rule with its neighbour so the order (first match wins)
// can be edited. Out-of-range indexes return the list unchanged.
export function moveRule(list: RuleForm[], index: number, delta: number): RuleForm[] {
  const to = index + delta
  if (index < 0 || to < 0 || index >= list.length || to >= list.length) return list
  const out = [...list]
  const [x] = out.splice(index, 1)
  out.splice(to, 0, x)
  return out
}

// bgpBadge is the colour for one BGP session: up is green, a session still
// trying is amber, anything else is red.
export function bgpBadge(n: { up: boolean; state: string }): 'ok' | 'warn' | 'bad' {
  if (n.up) return 'ok'
  if (n.state === 'Active' || n.state === 'Connect') return 'warn'
  return 'bad'
}

// ospfBadge is green when the adjacency is formed, amber while it is not.
export function ospfBadge(n: { full: boolean }): 'ok' | 'warn' {
  return n.full ? 'ok' : 'warn'
}

// frrRunning says whether the status card should speak of a running FRR. A box
// with neither protocol on has nothing to run and is not "starting".
export function frrRunning(st: RoutingStatus): boolean {
  return st.running || (!st.bgp_enabled && !st.ospf_enabled)
}

// formatUptime is how long a session has been up, coarse enough to read at a
// glance: "45 s", "12 min", "3 h", "4 d".
export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return ''
  if (seconds < 60) return t('routing.uptimeSeconds', { count: Math.floor(seconds) })
  if (seconds < 3600) return t('routing.uptimeMinutes', { count: Math.floor(seconds / 60) })
  if (seconds < 86400) return t('routing.uptimeHours', { count: Math.floor(seconds / 3600) })
  return t('routing.uptimeDays', { count: Math.floor(seconds / 86400) })
}

// sortRoutes orders the learned-routes table: the selected route of a prefix
// first, then by protocol and prefix.
export function sortRoutes(routes: LearnedRoute[]): LearnedRoute[] {
  return [...routes].sort((a, b) => {
    if (a.selected !== b.selected) return a.selected ? -1 : 1
    if (a.protocol !== b.protocol) return a.protocol < b.protocol ? -1 : 1
    return a.prefix.localeCompare(b.prefix, undefined, { numeric: true })
  })
}
