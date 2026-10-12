// Validation for the port forward, DHCP and DNS forms. Mirrors
// internal/config/validate_policy.go and validate_net.go; the server stays
// authoritative.
import type { DHCPServer, DNSSettings, PortForward } from '../../api/network'

export type Errors = Record<string, string>

// ipv4 returns the address as a number, or null if s is not dotted IPv4.
export function ipv4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim())
  if (!m) return null
  const parts = m.slice(1).map(Number)
  if (parts.some((p, i) => p > 255 || (m[i + 1].length > 1 && m[i + 1].startsWith('0')))) return null
  return ((parts[0] << 24) >>> 0) + (parts[1] << 16) + (parts[2] << 8) + parts[3]
}

// inSubnet reports whether ip is inside the CIDR prefix (e.g. 192.168.1.1/24).
export function inSubnet(ip: string, cidr: string): boolean {
  const [base, bitsStr] = cidr.split('/')
  const a = ipv4(ip)
  const b = ipv4(base)
  const bits = Number(bitsStr)
  if (a === null || b === null || !Number.isInteger(bits) || bits < 0 || bits > 32) return false
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0
  return ((a & mask) >>> 0) === ((b & mask) >>> 0)
}

// portOrRange parses "443" or "8000-8100" into [lo, hi].
export function portOrRange(s: string): [number, number] | null {
  const m = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(s.trim())
  if (!m) return null
  const lo = Number(m[1])
  const hi = m[2] === undefined ? lo : Number(m[2])
  if (lo < 1 || hi > 65535 || lo > hi) return null
  return [lo, hi]
}

const MAC = /^[0-9a-f]{2}(:[0-9a-f]{2}){5}$/i
const NAME = /^[A-Za-z0-9_.-]{1,63}$/ // validate_util.go nameRe
const HOST = /^(?=.{1,253}$)([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$/

export function validatePortForward(pf: PortForward, wanIfs: string[], others: PortForward[]): Errors {
  const e: Errors = {}
  if (!NAME.test(pf.name)) e.name = 'Name: letters, digits, . _ - (max 63)'
  else if (others.some((o) => o.name === pf.name)) e.name = 'Name is already used'
  if (!wanIfs.includes(pf.in_intf)) e.in_intf = 'Pick a WAN interface'
  if (pf.protocol !== 'tcp' && pf.protocol !== 'udp') e.protocol = 'Protocol must be tcp or udp'
  const ext = portOrRange(pf.ext_port)
  if (!ext) e.ext_port = 'Port 1-65535 or a range like 8000-8100'
  if (ipv4(pf.to_addr) === null) e.to_addr = 'Internal address must be IPv4'
  if (pf.to_port.trim() !== '') {
    const to = portOrRange(pf.to_port)
    if (!to) e.to_port = 'Port 1-65535 or a range like 8000-8100'
    else if (to[0] !== to[1] && ext && (to[0] !== ext[0] || to[1] !== ext[1]))
      e.to_port = 'A port range must equal the external range (or use one port)'
  }
  return e
}

export function validateDHCP(s: DHCPServer, subnet: string | undefined, others: DHCPServer[]): Errors {
  const e: Errors = {}
  if (!s.interface) e.interface = 'Pick an interface'
  else if (others.some((o) => o.interface === s.interface)) e.interface = 'This interface already has a DHCP server'
  const start = ipv4(s.range_start)
  const end = ipv4(s.range_end)
  if (start === null) e.range_start = 'Start must be IPv4'
  else if (subnet && !inSubnet(s.range_start, subnet)) e.range_start = `Outside ${subnet}`
  if (end === null) e.range_end = 'End must be IPv4'
  else if (subnet && !inSubnet(s.range_end, subnet)) e.range_end = `Outside ${subnet}`
  else if (start !== null && end < start) e.range_end = 'End is before start'
  if (s.lease_seconds !== undefined && (!Number.isInteger(s.lease_seconds) || s.lease_seconds < 0))
    e.lease_seconds = 'Lease time must be a whole number of seconds'
  if (s.gateway && ipv4(s.gateway) === null) e.gateway = 'Gateway must be IPv4'
  if ((s.dns ?? []).some((d) => ipv4(d) === null)) e.dns = 'DNS servers must be IPv4'
  const macs = new Set<string>()
  const ips = new Set<string>()
  ;(s.static_leases ?? []).forEach((l, i) => {
    const k = `static_leases.${i}`
    if (!MAC.test(l.mac)) e[k] = `Lease ${i + 1}: MAC must look like aa:bb:cc:dd:ee:ff`
    else if (ipv4(l.ip) === null) e[k] = `Lease ${i + 1}: IP must be IPv4`
    else if (subnet && !inSubnet(l.ip, subnet)) e[k] = `Lease ${i + 1}: ${l.ip} is outside ${subnet}`
    else if (macs.has(l.mac.toLowerCase())) e[k] = `Lease ${i + 1}: duplicate MAC`
    else if (ips.has(l.ip)) e[k] = `Lease ${i + 1}: duplicate IP`
    else if (l.hostname && !HOST.test(l.hostname)) e[k] = `Lease ${i + 1}: invalid hostname`
    macs.add(l.mac.toLowerCase())
    ips.add(l.ip)
  })
  return e
}

export function validateDNS(d: DNSSettings): Errors {
  const e: Errors = {}
  const bad = (d.forwarders ?? []).filter((f) => ipv4(f) === null)
  if (bad.length) e.forwarders = `Not IPv4: ${bad.join(', ')}`
  for (const [name, ip] of Object.entries(d.local_records ?? {})) {
    if (!HOST.test(name)) e[`record.${name}`] = `${name}: invalid host name`
    else if (ipv4(ip) === null) e[`record.${name}`] = `${name}: IP must be IPv4`
  }
  return e
}

// cleanDHCP drops empty optional fields (the server uses omitempty).
export function cleanDHCP(s: DHCPServer): DHCPServer {
  const out: DHCPServer = { interface: s.interface, range_start: s.range_start.trim(), range_end: s.range_end.trim() }
  if (s.lease_seconds) out.lease_seconds = s.lease_seconds
  if (s.dns?.length) out.dns = s.dns
  if (s.gateway?.trim()) out.gateway = s.gateway.trim()
  if (s.static_leases?.length)
    out.static_leases = s.static_leases.map((l) => (l.hostname ? l : { mac: l.mac, ip: l.ip }))
  return out
}

// Address, service and interface forms (validate_objects.go, validate_net.go).

export function validateAddress(a: { type: string; value: string }): Errors {
  const v = a.value.trim()
  const ok = (() => {
    switch (a.type) {
      case 'subnet': {
        const [ip, bits, extra] = v.split('/')
        return extra === undefined && ipv4(ip) !== null && /^\d{1,2}$/.test(bits ?? '') && Number(bits) <= 32
      }
      case 'host':
        return ipv4(v) !== null
      case 'range': {
        const [lo, hi, extra] = v.split('-')
        const a1 = ipv4(lo ?? ''), a2 = ipv4(hi ?? '')
        return extra === undefined && a1 !== null && a2 !== null && a1 <= a2
      }
      case 'fqdn':
        return HOST.test(v)
    }
    return false
  })()
  if (ok) return {}
  const want: Record<string, string> = {
    subnet: 'Enter an IPv4 subnet like 192.168.1.0/24',
    host: 'Enter an IPv4 address',
    range: 'Enter a range like 192.168.1.10-192.168.1.20',
    fqdn: 'Enter a hostname like example.com',
  }
  return { value: want[a.type] ?? 'Invalid value' }
}

export function validateService(s: { protocol: string; ports?: string }): Errors {
  if (s.protocol !== 'tcp' && s.protocol !== 'udp') return {}
  const parts = (s.ports ?? '').replace(/\s+/g, '').split(',')
  return parts.every((p) => portOrRange(p) !== null) ? {} : { ports: 'Use ports and ranges like 80,443,8000-8100' }
}

export interface Addressing {
  mode: string
  address?: string
  gateway?: string
  mtu?: number
}

export function validateAddressing(x: Addressing): Errors {
  const e: Errors = {}
  if (x.mode === 'static') {
    const [ip, bits, extra] = (x.address ?? '').split('/')
    if (extra !== undefined || ipv4(ip ?? '') === null || !/^\d{1,2}$/.test(bits ?? '') || Number(bits) > 32) {
      e.address = 'Enter the interface address with prefix, e.g. 192.168.1.1/24'
    } else if (x.gateway && !inSubnet(x.gateway, x.address!)) {
      e.gateway = 'The gateway must be inside the interface subnet'
    }
  }
  if (x.gateway && ipv4(x.gateway) === null) e.gateway = 'Enter an IPv4 address'
  if (x.mtu !== undefined && (x.mtu < 68 || x.mtu > 9216)) e.mtu = 'MTU must be 68-9216'
  return e
}

// validateGroup: a group needs members, and groups share the name space of
// the objects they collect (addresses or services).
export function validateGroup(g: { name: string; members?: string[] }, objects: string[], kind: string): Errors {
  const e: Errors = {}
  if ((g.members ?? []).length === 0) e.members = `Pick at least one ${kind}`
  if (objects.includes(g.name)) e.name = `A ${kind} has this name`
  return e
}
