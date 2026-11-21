// Network → RADIUS: the settings form (built into the `radius` section and
// checked like internal/config/radius.go does), and the filter of the
// authentication log.
import type { Candidate } from '../../api/objects'
import type { AuthEvent, RadiusConfig, UnknownChoice } from '../../api/radius'
import { t } from '../../i18n'

export const radiusWikiURL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/RADIUS-and-Dynamic-VLANs'

export interface ClientRow {
  name: string
  address: string
  secret: string // "" = unchanged for a stored client
  coaPort: string
  description: string
  stored: boolean // the client was saved before (its secret is kept when left empty)
}

export interface GroupRow {
  name: string
  vlan: string
}

export interface UserRow {
  name: string
  enabled: boolean
  password: string // "" = unchanged for a stored user
  group: string
  stored: boolean
}

export interface RadiusForm {
  enabled: boolean
  mab: boolean
  dot1x: boolean
  unknown: Exclude<UnknownChoice, ''>
  quarantineVlan: string
  clients: ClientRow[]
  groups: GroupRow[]
  users: UserRow[]
}

export function radiusFormOf(r: RadiusConfig | undefined): RadiusForm {
  return {
    enabled: r?.enabled ?? false,
    mab: r?.mab ?? false,
    dot1x: r?.dot1x ?? false,
    unknown: r?.unknown || 'reject',
    quarantineVlan: r?.quarantine_vlan ?? '',
    clients: (r?.clients ?? []).map((c) => ({
      name: c.name,
      address: c.address,
      secret: '',
      coaPort: c.coa_port ? String(c.coa_port) : '',
      description: c.description ?? '',
      stored: true,
    })),
    groups: (r?.groups ?? []).map((g) => ({ name: g.name, vlan: g.vlan ?? '' })),
    users: (r?.users ?? []).map((u) => ({ name: u.name, enabled: u.enabled, password: '', group: u.group ?? '', stored: true })),
  }
}

export const newClient = (): ClientRow => ({ name: '', address: '', secret: '', coaPort: '', description: '', stored: false })
export const newGroup = (): GroupRow => ({ name: '', vlan: '' })
export const newUser = (): UserRow => ({ name: '', enabled: true, password: '', group: '', stored: false })

// buildRadius turns the form into the section. An empty secret of a stored
// entry is sent empty: the server keeps the stored one (by name).
export function buildRadius(f: RadiusForm): RadiusConfig {
  const out: RadiusConfig = { enabled: f.enabled }
  if (f.mab) out.mab = true
  if (f.dot1x) out.dot1x = true
  if (f.unknown !== 'reject') out.unknown = f.unknown
  if (f.unknown === 'quarantine' && f.quarantineVlan) out.quarantine_vlan = f.quarantineVlan
  if (f.clients.length)
    out.clients = f.clients.map((c) => {
      const o: NonNullable<RadiusConfig['clients']>[number] = { name: c.name.trim(), address: c.address.trim() }
      if (c.secret) o.secret = c.secret
      if (c.coaPort.trim()) o.coa_port = Number(c.coaPort)
      if (c.description.trim()) o.description = c.description.trim()
      return o
    })
  if (f.groups.length) out.groups = f.groups.map((g) => (g.vlan ? { name: g.name.trim(), vlan: g.vlan } : { name: g.name.trim() }))
  if (f.users.length)
    out.users = f.users.map((u) => {
      const o: NonNullable<RadiusConfig['users']>[number] = { name: u.name.trim(), enabled: u.enabled }
      if (u.password) o.password = u.password
      if (u.group) o.group = u.group
      return o
    })
  return out
}

export type RadiusErrors = Record<string, string>

const nameRe = /^[A-Za-z][A-Za-z0-9_.-]{0,31}$/
const userRe = /^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,63}$/
const ipv4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/

// addressOK: an IPv4 or IPv6 address, optionally with a prefix length.
export function addressOK(s: string): boolean {
  const [ip, bits, extra] = s.trim().split('/')
  if (extra !== undefined || !ip) return false
  if (ipv4.test(ip)) return bits === undefined || (/^\d{1,2}$/.test(bits) && Number(bits) <= 32)
  if (ip.includes(':') && /^[0-9a-fA-F:.]+$/.test(ip) && (ip.match(/::/g) ?? []).length <= 1)
    return bits === undefined || (/^\d{1,3}$/.test(bits) && Number(bits) <= 128)
  return false
}

// secretOK: printable ASCII without spaces, quotes or backslashes.
export const secretOK = (s: string) => /^[\x21-\x7e]+$/.test(s) && !/["'`\\]/.test(s)

// validateRadius checks the form; keys are the section's JSON paths.
export function validateRadius(f: RadiusForm, vlans: string[], caEnabled: boolean): RadiusErrors {
  const e: RadiusErrors = {}
  if (f.enabled && f.clients.length === 0) e['radius.clients'] = t('radius.errNoClients')
  if (f.enabled && !f.mab && !f.dot1x) e['radius.mab'] = t('radius.errNothingOn')
  if (f.dot1x && !caEnabled) e['radius.dot1x'] = t('radius.errNeedsCA')
  if (f.unknown === 'quarantine' && !f.quarantineVlan) e['radius.quarantine_vlan'] = t('radius.errPickQuarantine')
  const seen = new Set<string>()
  f.clients.forEach((c, i) => {
    const p = `radius.clients[${i}]`
    if (!nameRe.test(c.name.trim())) e[`${p}.name`] = t('radius.errName')
    else if (seen.has(c.name.trim())) e[`${p}.name`] = t('radius.errDuplicate')
    seen.add(c.name.trim())
    if (!addressOK(c.address)) e[`${p}.address`] = t('radius.errAddress')
    if (c.secret ? c.secret.length < 8 || !secretOK(c.secret) : !c.stored) e[`${p}.secret`] = t('radius.errSecret')
    const port = c.coaPort.trim()
    if (port && !(/^\d+$/.test(port) && Number(port) >= 1 && Number(port) <= 65535)) e[`${p}.coa_port`] = t('radius.errPort')
  })
  const groups = new Set<string>()
  f.groups.forEach((g, i) => {
    const p = `radius.groups[${i}]`
    if (!nameRe.test(g.name.trim())) e[`${p}.name`] = t('radius.errName')
    else if (groups.has(g.name.trim())) e[`${p}.name`] = t('radius.errDuplicate')
    groups.add(g.name.trim())
    if (g.vlan && !vlans.includes(g.vlan)) e[`${p}.vlan`] = t('radius.errVlan')
  })
  const users = new Set<string>()
  f.users.forEach((u, i) => {
    const p = `radius.users[${i}]`
    const n = u.name.trim().toLowerCase()
    if (!userRe.test(u.name.trim())) e[`${p}.name`] = t('radius.errUserName')
    else if (users.has(n)) e[`${p}.name`] = t('radius.errDuplicate')
    users.add(n)
    if (u.password ? u.password.length < 8 : u.enabled && !u.stored) e[`${p}.password`] = t('radius.errPassword')
    if (u.group && !groups.has(u.group)) e[`${p}.group`] = t('radius.errGroup')
  })
  return e
}

// vlanNames lists the candidate's VLANs.
export const vlanNames = (cfg: Candidate | null | undefined) => (cfg?.vlans ?? []).map((v) => v.name)

export type ResultFilter = '' | 'accept' | 'quarantine' | 'reject' | 'disconnect'

// filterEvents keeps the authentications matching the text (MAC in any
// notation, user, switch, port, VLAN, reason) and the result.
export function filterEvents(events: AuthEvent[], text: string, result: ResultFilter): AuthEvent[] {
  const q = text.trim().toLowerCase()
  const qMac = q.replace(/[^0-9a-f]/g, '')
  return events.filter((e) => {
    if (result === 'disconnect' ? e.method !== 'disconnect' : result && e.result !== result) return false
    if (!q) return true
    if (qMac.length >= 4 && (e.mac ?? '').includes(qMac)) return true
    return [e.user, e.client, e.nas, e.port, e.vlan, e.reason, e.method].some((v) => (v ?? '').toLowerCase().includes(q))
  })
}

// colonMAC shows an inventory MAC (12 hex) as aa:bb:cc:dd:ee:ff.
export function colonMAC(m: string | undefined): string {
  if (!m || m.length !== 12) return m ?? ''
  return m.match(/../g)!.join(':')
}

// pemDownload is a link target for a PEM file.
export const pemDownload = (pem: string) => `data:application/x-pem-file;charset=utf-8,${encodeURIComponent(pem)}`
