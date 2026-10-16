// Validation for the port forward, DHCP and DNS forms. Mirrors
// internal/config/validate_policy.go and validate_net.go; the server stays
// authoritative.
import type { DHCPServer, DNSSettings, PortForward } from '../../api/network'
import type { Candidate, Gateway, GatewayGroup, IPv6, Route, WireGuardClient } from '../../api/objects'

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

// IPv6 helpers ----------------------------------------------------------------

const V6 = /^[0-9a-f:]+$/i

function isV6(s: string): boolean {
  const t = s.trim()
  if (!V6.test(t)) return false
  // Allow ::, single group, or expanded form
  const parts = t.split(':')
  if (parts.length < 2 || parts.length > 8) return false
  let doubles = 0
  for (const p of parts) {
    if (p === '') {
      doubles++
      continue
    }
    if (!/^[0-9a-f]{1,4}$/i.test(p)) return false
  }
  return true
}

// isV6Host reports whether s is a bare IPv6 address (no prefix).
export function isV6Host(s: string): boolean {
  const t = s.trim()
  if (t.includes('/')) return false
  if (!isV6(t)) return false
  // Reject 4-in-6 like ::ffff:192.168.1.1 (contains dots)
  if (t.includes('.')) return false
  return true
}

// v6PrefixBits returns the prefix length from a "addr/bits" string, or null.
export function v6PrefixBits(s: string): number | null {
  const [ip, bitsStr, extra] = s.trim().split('/')
  if (extra !== undefined || !isV6Host(ip ?? '')) return null
  const bits = Number(bitsStr)
  if (!Number.isInteger(bits) || bits < 1 || bits > 128) return null
  return bits
}

// isLinkLocalV6 reports whether an IPv6 address is in fe80::/10.
export function isLinkLocalV6(s: string): boolean {
  const t = s.trim().toLowerCase()
  if (t.startsWith('fe80:') || t.startsWith('fe80::')) return true
  // Handle compressed forms like fe80::1
  if (t.startsWith('fe80') && t.includes(':')) return true
  return false
}

// isV4In6 reports whether an IPv6 string embeds an IPv4 part (dotted quad).
export function isV4In6(s: string): boolean {
  return s.trim().includes('.')
}

// v6Range validates "lo-hi" where both ends are IPv6 in the same family.
export function v6RangeValid(s: string): boolean {
  const [lo, hi, extra] = s.trim().split('-')
  if (extra !== undefined) return false
  if (!isV6Host(lo ?? '') || !isV6Host(hi ?? '')) return false
  // Both must be pure v6 (no embedded v4) or both embedded — same family.
  const loIs4 = isV4In6(lo)
  const hiIs4 = isV4In6(hi)
  return loIs4 === hiIs4
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
        // Try IPv4 first, then IPv6.
        const [ip, bits, extra] = v.split('/')
        if (extra === undefined && ipv4(ip) !== null && /^\d{1,2}$/.test(bits ?? '') && Number(bits) <= 32) return true
        return v6PrefixBits(v) !== null
      }
      case 'host':
        return ipv4(v) !== null || isV6Host(v)
      case 'range': {
        // IPv4 range
        const [lo, hi, extra] = v.split('-')
        if (extra === undefined) {
          const a1 = ipv4(lo ?? ''), a2 = ipv4(hi ?? '')
          if (a1 !== null && a2 !== null && a1 <= a2) return true
        }
        // IPv6 range (both ends same family, no mixed)
        return v6RangeValid(v)
      }
      case 'fqdn':
        return HOST.test(v)
    }
    return false
  })()
  if (ok) return {}
  const want: Record<string, string> = {
    subnet: 'Enter an IPv4 or IPv6 subnet like 192.168.1.0/24 or 2001:db8::/32',
    host: 'Enter an IPv4 or IPv6 address',
    range: 'Enter a range like 192.168.1.10-192.168.1.20 or 2001:db8::1-2001:db8::ff',
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

// validateIPv6 checks the ipv6 block of an interface or VLAN. role is the
// interface's own role; for a VLAN, pass the parent's role. cfg is used to
// find valid uplinks for delegated mode. Mirrors internal/config/validate_ipv6.go.
export function validateIPv6(v: IPv6 | undefined, role: string, cfg: Candidate): Errors {
  if (!v || v.mode === 'none') return {}
  const e: Errors = {}
  const wan = role === 'wan'

  // Mode constraints
  if (v.mode === 'dhcp6' && !wan) e.mode = 'DHCPv6 is only available on WAN interfaces'
  if (v.mode === 'delegated' && wan) e.mode = 'Delegated mode is not available on WAN interfaces'

  // Static: address required, must be a valid IPv6 CIDR
  if (v.mode === 'static') {
    if (!v.address?.trim()) {
      e.ipv6_address = 'Enter the interface address with prefix, e.g. 2001:db8::1/64'
    } else {
      const bits = v6PrefixBits(v.address)
      if (bits === null) e.ipv6_address = 'Enter a valid IPv6 address with prefix, e.g. 2001:db8::1/64'
      else if (isLinkLocalV6(v.address)) e.ipv6_address = 'Link-local addresses are not allowed as the interface address'
      else if (isV4In6(v.address)) e.ipv6_address = 'Embedded IPv4 addresses are not allowed'
      else if (!wan && v.ra?.enabled !== false && bits !== 64) e.ipv6_address = 'Non-WAN static with RA enabled must use a /64 prefix'
    }
    // Gateway: only on WAN, must be in prefix or link-local
    if (v.gateway?.trim()) {
      if (!wan) e.ipv6_gateway = 'Gateway is only available on WAN interfaces'
      else if (v.address && v6PrefixBits(v.address) !== null) {
        const gwHost = v.gateway.split('/')[0] ?? v.gateway
        if (!isLinkLocalV6(gwHost) && !inV6Subnet(gwHost, v.address)) e.ipv6_gateway = 'Gateway must be in the interface prefix or link-local'
      }
    }
  }

  // DHCP6: pd_size 48-64
  if (v.mode === 'dhcp6') {
    if (v.pd_size !== undefined && (v.pd_size < 48 || v.pd_size > 64)) e.ipv6_pd_size = 'Prefix delegation size must be 48-64'
  }

  // Delegated: uplink required, prefix_id optional
  if (v.mode === 'delegated') {
    const uplinks = [
      ...(cfg.interfaces ?? []).filter((i) => i.ipv6?.mode === 'dhcp6').map((i) => i.name),
      ...(cfg.vlans ?? []).filter((vl) => vl.ipv6?.mode === 'dhcp6').map((vl) => vl.name),
    ]
    if (v.uplink !== undefined && v.uplink !== '' && !uplinks.includes(v.uplink)) e.ipv6_uplink = 'Pick a DHCPv6 interface as uplink'
    if (v.prefix_id !== undefined && (!Number.isInteger(v.prefix_id) || v.prefix_id < 0)) e.ipv6_prefix_id = 'Prefix ID must be a non-negative integer'
  }

  // RA: only on static/delegated
  if (v.ra && v.mode !== 'static' && v.mode !== 'delegated') e.ipv6_ra = 'RA is only available in static or delegated mode'

  return e
}

// inV6Subnet checks whether an IPv6 host address is within a CIDR prefix.
// Simplified: compares the hex groups up to the prefix length.
function inV6Subnet(ip: string, cidr: string): boolean {
  const [base, bitsStr] = cidr.split('/')
  const bits = Number(bitsStr)
  if (!Number.isInteger(bits) || bits < 1 || bits > 128) return false
  const a = expandV6(ip.trim())
  const b = expandV6(base.trim())
  if (a === null || b === null) return false
  // Compare the first `bits` bits
  const fullGroups = Math.floor(bits / 4)
  const remBits = bits % 4
  for (let i = 0; i < fullGroups; i++) {
    if (a[i] !== b[i]) return false
  }
  if (remBits > 0 && fullGroups < 8) {
    const mask = (0xffff << (16 - remBits)) & 0xffff
    if ((a[fullGroups] & mask) !== (b[fullGroups] & mask)) return false
  }
  return true
}

// Multi-WAN gateways, groups and routes (task 049/050). Mirrors
// internal/config/routing.go.

// wanInterfaces returns every interface usable as a gateway uplink: WAN
// interfaces and VLANs whose parent is WAN.
export function wanInterfaces(cfg: Candidate): string[] {
  const wans = new Set((cfg.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
  return [
    ...wans,
    ...(cfg.vlans ?? []).filter((v) => wans.has(v.parent)).map((v) => v.name),
  ]
}

// wireGuardClients returns the tunnel interface names usable as gateways.
export function wireGuardClients(cfg: Candidate): string[] {
  return (cfg.vpn?.wireguard_clients ?? []).map((c) => c.name)
}

export function validateGateway(g: Gateway, cfg: Candidate): Errors {
  const e: Errors = {}
  const wans = wanInterfaces(cfg)
  const clients = wireGuardClients(cfg)
  if (!g.interface) e.interface = 'Pick an interface'
  else if (clients.includes(g.interface)) {
    if (g.address?.trim()) e.address = 'A tunnel gateway has no address'
  } else if (wans.includes(g.interface)) {
    const addr = g.address?.trim() ?? ''
    if (addr !== 'dhcp' && ipv4(addr) === null) e.address = 'Enter the router IPv4 address or "dhcp"'
  } else {
    e.interface = 'Pick a WAN interface or WireGuard client'
  }
  const ip = g.monitor?.ip?.trim()
  if (ip) {
    if (ipv4(ip) === null) {
      e['monitor.ip'] = 'Monitor IP must be IPv4'
    } else {
      const dup = (cfg.gateways ?? []).find((o) => o.name !== g.name && o.monitor?.ip?.trim() === ip)
      if (dup) e['monitor.ip'] = `Already monitored through ${dup.name}: each gateway needs its own address`
    }
  }
  const m = g.monitor
  if (m) {
    const iv = m.interval_seconds ?? 0
    const loss = m.loss_percent ?? 0
    const lat = m.latency_ms ?? 0
    if (iv < 0 || iv > 60 || loss < 0 || loss > 100 || lat < 0) e.monitor = 'Interval 1-60 s, loss 0-100 %, latency ≥ 0 ms'
  }
  return e
}

export function validateGatewayGroup(gg: GatewayGroup, cfg: Candidate): Errors {
  const e: Errors = {}
  if (gg.members.length === 0) e.members = 'A group needs at least one gateway'
  const known = new Set((cfg.gateways ?? []).map((g) => g.name))
  const seen = new Set<string>()
  gg.members.forEach((m, i) => {
    const k = `members.${i}`
    if (!known.has(m.gateway)) e[`${k}.gateway`] = `Unknown gateway ${m.gateway}`
    else if (seen.has(m.gateway)) e[`${k}.gateway`] = `${m.gateway} is listed twice`
    seen.add(m.gateway)
    const tier = m.tier ?? 1
    const weight = m.weight ?? 1
    if (tier < 1 || tier > 5 || weight < 1 || weight > 100) e[k] = 'tier 1-5, weight 1-100'
  })
  return e
}

export function validateRoute(r: Route, cfg: Candidate): Errors {
  const e: Errors = {}
  const [ip, bits, extra] = (r.destination ?? '').trim().split('/')
  const b = Number(bits)
  if (extra !== undefined || ipv4(ip ?? '') === null || !Number.isInteger(b) || b < 0 || b > 32) {
    e.destination = 'Enter an IPv4 subnet like 192.168.2.0/24'
  }
  if (!r.gateway) e.gateway = 'Pick a gateway'
  else if (!(cfg.gateways ?? []).some((g) => g.name === r.gateway)) e.gateway = 'A route needs a gateway (not a group)'
  return e
}

// WireGuard client (VPN provider) tunnels: mirror internal/config/routing.go
// (checkRouting) and validWGKey.
const WG_NAME = /^[a-z0-9_-]{1,15}$/

function isWGKey(s: string): boolean {
  // A 32-byte key base64-encodes to 43 chars + "=".
  const t = s.trim()
  return t.length === 44 && t.endsWith('=') && /^[A-Za-z0-9+/]{43}=$/.test(t)
}

export function validateWireGuardClient(w: WireGuardClient): Errors {
  const e: Errors = {}
  if (!WG_NAME.test(w.name)) e.name = 'Device name: 1-15 of a-z, 0-9, _ and -'
  const [ip, bits, extra] = (w.address ?? '').trim().split('/')
  if (extra !== undefined || ipv4(ip ?? '') === null || bits !== '32') e.address = 'Enter the tunnel IPv4/32 from the provider, e.g. 10.64.12.34/32'
  if (!isWGKey(w.peer_public_key)) e.peer_public_key = 'Enter the provider public key (base64, 32 bytes)'
  const [host, port, more] = (w.endpoint ?? '').split(':')
  if (more !== undefined || !host || !port) e.endpoint = 'Enter host:port, e.g. se-sto-wg-001.example.net:51820'
  if (w.mtu !== undefined && (w.mtu < 1280 || w.mtu > 1500)) e.mtu = 'MTU must be 1280-1500'
  return e
}

// expandV6 expands an IPv6 address to 8 groups of hex numbers, or null.
function expandV6(s: string): number[] | null {
  if (!isV6Host(s)) return null
  let t = s.toLowerCase()
  // Handle :: expansion
  const parts = t.split('::')
  if (parts.length > 2) return null
  const left = parts[0] ? parts[0].split(':').filter(Boolean) : []
  const right = parts.length === 2 && parts[1] ? parts[1].split(':').filter(Boolean) : []
  if (parts.length === 2 && left.length + right.length > 7) return null
  const missing = 8 - left.length - right.length
  if (missing < 0) return null
  const groups = [...left, ...(parts.length === 2 ? Array(missing).fill('0') : []), ...right]
  if (groups.length !== 8) return null
  return groups.map((g) => {
    if (!/^[0-9a-f]{1,4}$/.test(g)) return NaN
    return parseInt(g, 16)
  })
}
