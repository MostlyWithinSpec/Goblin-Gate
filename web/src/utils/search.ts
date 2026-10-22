// Global search (Ctrl+K): an index of the menu pages and the candidate
// config's named objects, and a scorer that ranks them for a query. Object
// entries open the object's edit drawer where the page supports ?edit=.
import type { Candidate } from '../api/objects'

export interface SearchItem {
  id: string // unique, also the key for recent picks
  type: string // shown as a label: "Page", "Policy", "Address", ...
  name: string
  detail?: string // a value shown next to the name and searched too
  path: string
}

// PAGES: every page reachable from the menu, with the words people use.
const PAGES: { name: string; path: string; words?: string }[] = [
  { name: 'Dashboard', path: '/' },
  { name: 'Goblin Checks', path: '/checks', words: 'score audit findings fix security health' },
  { name: 'Interfaces & VLANs', path: '/network/interfaces', words: 'wan lan vlan' },
  { name: 'Addresses', path: '/network/addresses', words: 'objects hosts subnets country geoip' },
  { name: 'Address groups', path: '/network/address-groups' },
  { name: 'Services', path: '/network/services', words: 'ports' },
  { name: 'Service groups', path: '/network/service-groups' },
  { name: 'Port forwards', path: '/network/port-forwards', words: 'nat dnat virtual ip' },
  { name: 'UPnP', path: '/network/upnp', words: 'nat-pmp pcp' },
  { name: 'DHCP', path: '/network/dhcp', words: 'leases static' },
  { name: 'DNS', path: '/network/dns', words: 'resolver unbound' },
  { name: 'Gateways', path: '/network/gateways', words: 'multi-wan failover' },
  { name: 'Gateway groups', path: '/network/gateway-groups' },
  { name: 'Static routes', path: '/network/static-routes' },
  { name: 'Devices', path: '/network/devices', words: 'clients wake on lan' },
  { name: 'Dynamic DNS', path: '/network/dynamic-dns', words: 'ddns duckdns cloudflare' },
  { name: 'Firewall policies', path: '/policy', words: 'rules simulator' },
  { name: 'Schedules', path: '/policy/schedules' },
  { name: 'Custom rules', path: '/policy/custom-rules', words: 'nftables nft escape hatch' },
  { name: 'Templates', path: '/templates', words: 'guest wifi iot bedtime port forward vpn wizard presets' },
  { name: 'Security profiles', path: '/security' },
  { name: 'Threat feeds', path: '/security/feeds', words: 'blocklists' },
  { name: 'DNS filter', path: '/security/dns-filter', words: 'ads blocking' },
  { name: 'Web filter', path: '/security/web-filter', words: 'categories' },
  { name: 'Application control', path: '/security/apps' },
  { name: 'IPS', path: '/security/ips', words: 'suricata intrusion' },
  { name: 'CrowdSec', path: '/security/crowdsec', words: 'bouncer lapi bans' },
  { name: 'Tailscale', path: '/vpn/tailscale', words: 'vpn' },
  { name: 'WireGuard', path: '/vpn/wireguard', words: 'vpn peers' },
  { name: 'VPN providers', path: '/vpn/providers' },
  { name: 'Logs', path: '/log', words: 'log report traffic events' },
  { name: 'Live traffic', path: '/log/live', words: 'flows bandwidth' },
  { name: 'Packet capture', path: '/log/capture', words: 'pcap tcpdump' },
  { name: 'Live events', path: '/events', words: 'stream sse commits logins' },
  { name: 'Log export', path: '/log/export', words: 'syslog loki' },
  { name: 'System', path: '/system', words: 'admins tokens backup import' },
  { name: 'Revisions', path: '/system/revisions', words: 'history rollback' },
  { name: 'Notifications', path: '/system/notifications', words: 'alerts email' },
  { name: 'Plugins', path: '/system/plugins' },
  { name: 'Updates', path: '/system/updates' },
  { name: 'Diagnostics', path: '/system/diagnostics', words: 'ping traceroute dns lookup' },
  { name: 'UPS', path: '/system/ups', words: 'power failure battery nut ups shut down runtime charge' },
  { name: 'Snapshots', path: '/system/snapshots' },
  { name: 'Generated config', path: '/system/generated' },
]

const edit = (base: string, name: string) => `${base}?edit=${encodeURIComponent(name)}`

// buildSearchIndex lists the pages and the candidate's objects.
export function buildSearchIndex(cfg: Candidate): SearchItem[] {
  const items: SearchItem[] = PAGES.map((p) => ({ id: 'page:' + p.path, type: 'Page', name: p.name, detail: p.words, path: p.path }))
  const add = (type: string, list: { name: string }[] | undefined, base: string, detail?: (x: any) => string | undefined) => {
    for (const x of list ?? []) {
      items.push({ id: `${type}:${x.name}`, type, name: x.name, detail: detail?.(x), path: edit(base, x.name) })
    }
  }
  for (const p of cfg.policies ?? []) {
    items.push({ id: `Policy:${p.id}`, type: 'Policy', name: p.name || `Policy ${p.id}`, detail: `#${p.id}`, path: `/policy?edit=${p.id}` })
  }
  add('Address', cfg.addresses, '/network/addresses', (a) => a.value)
  add('Address group', cfg.address_groups, '/network/address-groups', (g) => g.members?.join(' '))
  add('Service', cfg.services, '/network/services', (s) => [s.protocol, s.ports].filter(Boolean).join(' '))
  add('Service group', cfg.service_groups, '/network/service-groups', (g) => g.members?.join(' '))
  add('Interface', cfg.interfaces, '/network/interfaces', (i) => i.device)
  for (const v of cfg.vlans ?? []) {
    items.push({ id: `VLAN:${v.name}`, type: 'VLAN', name: v.name, detail: `VLAN ${v.vlan_id ?? ''}`.trim(), path: '/network/interfaces' })
  }
  add('Port forward', cfg.nat?.port_forwards, '/network/port-forwards', (f) => [f.ext_port, f.to_addr].filter(Boolean).join(' '))
  add('Schedule', cfg.schedules, '/policy/schedules')
  add('Custom rule', cfg.custom_rules, '/policy/custom-rules', (r) => r.rule)
  add('Gateway', cfg.gateways, '/network/gateways', (g) => g.monitor_ip ?? g.interface)
  add('Gateway group', cfg.gateway_groups, '/network/gateway-groups')
  add('Dynamic DNS', cfg.ddns, '/network/dynamic-dns', (d) => d.hostname)
  add('Threat feed', cfg.feeds, '/security/feeds', (f) => f.url)
  for (const r of cfg.routes ?? []) {
    items.push({ id: `Route:${r.destination}`, type: 'Static route', name: r.destination, detail: r.gateway, path: '/network/static-routes' })
  }
  for (const d of cfg.dhcp ?? []) {
    for (const l of d.static_leases ?? []) {
      const name = l.hostname || l.ip || l.mac
      items.push({ id: `Lease:${l.mac}`, type: 'DHCP lease', name, detail: [l.mac, l.ip].filter(Boolean).join(' '), path: '/network/dhcp' })
    }
  }
  return items
}

// score rates how well text matches the query (0 = no match): the whole
// text, a prefix, a word start, a substring, then the letters in order.
export function score(text: string, query: string): number {
  const t = text.toLowerCase()
  const q = query.toLowerCase()
  if (!q) return 0
  if (t === q) return 100
  if (t.startsWith(q)) return 80
  if (t.split(/[\s._/-]+/).some((w) => w.startsWith(q))) return 70
  if (t.includes(q)) return 50
  let i = 0
  for (const c of t) {
    if (c === q[i]) i++
    if (i === q.length) return 20
  }
  return 0
}

// search ranks the index for a query: the name counts fully, the type and
// detail less; pages win ties. At most limit results.
export function search(items: SearchItem[], query: string, limit = 30): SearchItem[] {
  const q = query.trim()
  if (!q) return []
  const scored: { item: SearchItem; s: number }[] = []
  for (const item of items) {
    const s = Math.max(score(item.name, q), score(item.detail ?? '', q) * 0.6, score(item.type, q) * 0.4)
    if (s > 0) scored.push({ item, s: s + (item.type === 'Page' ? 1 : 0) })
  }
  scored.sort((a, b) => b.s - a.s || a.item.name.localeCompare(b.item.name))
  return scored.slice(0, limit).map((x) => x.item)
}
