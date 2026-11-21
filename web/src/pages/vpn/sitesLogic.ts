// Pure logic of VPN → Sites (task 156): the settings form, the checks the
// server makes too (so a message shows before saving), status badges and
// the numbers in the table.
import type { Candidate } from '../../api/objects'
import type { Site, SitesSettings, SiteStatus } from '../../api/sites'

export const sitesWikiURL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Site-to-Site-VPN'

// SettingsForm is this gate's own part of the section; remotes are edited
// one by one and kept as they are when the settings are saved.
export interface SettingsForm {
  enabled: boolean
  endpoint: string
  networks: string[]
  https: boolean
  ssh: boolean
  announce: boolean
}

export function settingsOf(s?: SitesSettings): SettingsForm {
  const access = s?.allow_access ?? []
  return {
    enabled: s?.enabled ?? false,
    endpoint: s?.endpoint ?? '',
    networks: [...(s?.networks ?? [])],
    https: access.includes('https'),
    ssh: access.includes('ssh'),
    announce: s?.announce ?? false,
  }
}

// buildSettings merges the form into the stored section: the tunnel address
// and the remotes stay as stored (the address is picked by the first pairing).
export function buildSettings(f: SettingsForm, stored?: SitesSettings): SitesSettings {
  const out: SitesSettings = { ...(stored ?? { enabled: false }), enabled: f.enabled }
  out.endpoint = f.endpoint.trim() || undefined
  out.networks = f.networks.length > 0 ? [...f.networks] : undefined
  const access = [f.https ? 'https' : '', f.ssh ? 'ssh' : ''].filter(Boolean)
  out.allow_access = access.length > 0 ? access : undefined
  out.announce = f.announce || undefined
  return out
}

// networkOptions are what this gate can offer: address objects of type host
// or subnet and interfaces/VLANs (the same choice as BGP's announce list).
export function networkOptions(cfg?: Candidate): string[] {
  const objs = (cfg?.addresses ?? []).filter((a) => a.type === 'host' || a.type === 'subnet').map((a) => a.name)
  const ifaces = (cfg?.interfaces ?? []).filter((i) => i.role !== 'wan').map((i) => i.name)
  const vlans = (cfg?.vlans ?? []).map((v) => v.name)
  return [...ifaces, ...vlans, ...objs]
}

const siteNameRe = /^[a-z][a-z0-9-]{0,14}$/
export const validSiteName = (s: string) => siteNameRe.test(s)

const hostRe = /^(?=.{1,253}$)([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/
export function validHost(s: string): boolean {
  return hostRe.test(s) || validIPv4(s) || (s.includes(':') && /^[0-9a-fA-F:.]+$/.test(s))
}

export function validIPv4(s: string): boolean {
  const p = s.split('.')
  return p.length === 4 && p.every((x) => /^\d{1,3}$/.test(x) && Number(x) <= 255 && String(Number(x)) === x)
}

export function validHostPort(s: string): boolean {
  const m = /^(\[[0-9a-fA-F:.]+\]|[^:[\]]+):(\d{1,5})$/.exec(s)
  if (!m) return false
  const port = Number(m[2])
  return port >= 1 && port <= 65535 && validHost(m[1].replace(/^\[|\]$/g, ''))
}

// validCIDR: IPv4 network without host bits (what the server takes for a site).
export function validCIDR(s: string): boolean {
  const [a, b] = s.split('/')
  if (!a || b === undefined || !validIPv4(a) || !/^\d{1,2}$/.test(b)) return false
  const bits = Number(b)
  if (bits < 1 || bits > 32) return false
  const n = a.split('.').reduce((acc, x) => acc * 256 + Number(x), 0)
  const mask = bits === 32 ? 0 : 2 ** (32 - bits)
  return n % mask === 0 || bits === 32
}

export interface SettingsErrors {
  endpoint?: string
}

export function validateSettings(f: SettingsForm): SettingsErrors {
  const e: SettingsErrors = {}
  if (f.endpoint.trim() && !validHost(f.endpoint.trim())) e.endpoint = 'endpoint'
  return e
}

// SiteForm edits one remote site. Numbers stay strings while typed.
export interface SiteForm {
  name: string
  enabled: boolean
  description: string
  endpoint: string
  keepalive: string
  via: string
  address: string
  networks: string // one CIDR per line or comma-separated
}

export function siteFormOf(s?: Site): SiteForm {
  return {
    name: s?.name ?? '',
    enabled: s?.enabled ?? true,
    description: s?.description ?? '',
    endpoint: s?.endpoint ?? '',
    keepalive: s?.keepalive ? String(s.keepalive) : '',
    via: s?.via ?? '',
    address: s?.address ?? '',
    networks: (s?.networks ?? []).join('\n'),
  }
}

export const splitNetworks = (s: string) =>
  s
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean)

// buildSite applies the form to the stored site. A paired site keeps its key,
// port and tunnel address; a site reached through another one has only a name,
// via, address and networks.
export function buildSite(f: SiteForm, stored?: Site): Site {
  const out: Site = { ...(stored ?? { name: f.name, enabled: true }), name: f.name.trim(), enabled: f.enabled }
  out.description = f.description.trim() || undefined
  const keep = Number(f.keepalive)
  out.keepalive = f.keepalive.trim() && keep > 0 ? keep : undefined
  out.networks = splitNetworks(f.networks)
  if (f.via) {
    out.via = f.via
    out.address = f.address.trim()
    delete out.public_key
    delete out.endpoint
    delete out.listen_port
  } else {
    delete out.via
    out.endpoint = f.endpoint.trim() || undefined
  }
  return out
}

export interface SiteErrors {
  name?: string
  endpoint?: string
  keepalive?: string
  address?: string
  networks?: string
  via?: string
}

// validateSite checks one site the way config.checkSites does; isNew forbids a
// name already used by another site or interface.
export function validateSite(f: SiteForm, others: Site[], taken: string[], isNew: boolean): SiteErrors {
  const e: SiteErrors = {}
  const name = f.name.trim()
  if (!validSiteName(name)) e.name = 'name'
  else if (isNew && (others.some((s) => s.name === name) || taken.includes(name))) e.name = 'taken'
  if (f.endpoint.trim() && !validHostPort(f.endpoint.trim())) e.endpoint = 'endpoint'
  if (f.keepalive.trim() && !(/^\d+$/.test(f.keepalive.trim()) && Number(f.keepalive) <= 3600)) e.keepalive = 'keepalive'
  const bad = splitNetworks(f.networks).find((n) => !validCIDR(n))
  if (bad) e.networks = bad
  if (f.via) {
    if (!validIPv4(f.address.trim())) e.address = 'address'
    const hub = others.find((s) => s.name === f.via)
    if (!hub || hub.via) e.via = 'via'
    if (splitNetworks(f.networks).length === 0) e.networks = e.networks ?? ''
  }
  return e
}

// hubs are the sites another site can be reached through: those with a tunnel.
export const hubs = (remotes: Site[], except?: string) => remotes.filter((s) => !s.via && s.name !== except).map((s) => s.name)

export type BadgeKind = 'ok' | 'warn' | 'bad' | 'off'

// siteBadge: reachable → green; a handshake but no ping answer → amber; no
// handshake within 3 minutes or no answer → red; disabled or not committed → grey.
export function siteBadge(st: SiteStatus | undefined, enabled: boolean): { kind: BadgeKind; key: 'reachable' | 'noAnswer' | 'noHandshake' | 'disabled' | 'notRunning' } {
  if (!enabled) return { kind: 'off', key: 'disabled' }
  if (!st) return { kind: 'off', key: 'notRunning' }
  if (st.reachable) return { kind: 'ok', key: 'reachable' }
  if (!st.via && (st.handshake_age < 0 || st.handshake_age > 180)) return { kind: 'bad', key: 'noHandshake' }
  if (st.reachable === false) return st.via ? { kind: 'bad', key: 'noAnswer' } : { kind: 'warn', key: 'noAnswer' }
  return { kind: 'off', key: 'notRunning' }
}

export function formatBytes(n: number): string {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

// formatAge turns seconds into "42 s", "5 min", "3 h", "2 d"; "" for never.
export function formatAge(sec: number): string {
  if (sec < 0) return ''
  if (sec < 90) return `${sec} s`
  if (sec < 90 * 60) return `${Math.round(sec / 60)} min`
  if (sec < 36 * 3600) return `${Math.round(sec / 3600)} h`
  return `${Math.round(sec / 86400)} d`
}

// minutesLeft until an ISO time (0 when past).
export function minutesLeft(iso: string, now = Date.now()): number {
  return Math.max(0, Math.round((Date.parse(iso) - now) / 60000))
}

// zoneNames are the site zones policies can use: enabled sites with their own
// tunnel while sites are on (task 155's config.DirectSites).
export function zoneNames(s?: SitesSettings): string[] {
  if (!s?.enabled) return []
  return (s.remotes ?? []).filter((r) => r.enabled && !r.via).map((r) => r.name)
}

// takenNames: interface, VLAN and VPN names on this gate a new site cannot use.
export function takenNames(cfg?: Candidate): string[] {
  return [
    ...(cfg?.interfaces ?? []).map((i) => i.name),
    ...(cfg?.vlans ?? []).map((v) => v.name),
    ...(cfg?.vpn?.wireguard ?? []).map((w) => w.name),
    ...(cfg?.vpn?.wireguard_clients ?? []).map((w) => w.name),
    'tailscale',
  ]
}
