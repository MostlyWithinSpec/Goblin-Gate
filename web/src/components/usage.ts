// usage: where a named object of the candidate config is referenced, so a
// list page can refuse to delete it while something still uses it (the
// server would only reject the next commit, far from the cause).
import type { Candidate } from '../api/objects'
import type { Policy } from '../api/policy'

// policyFields: the policy fields that reference entries of each list path.
const policyFields: Record<string, (p: Policy) => (string | undefined)[]> = {
  addresses: (p) => [...(p.src_addr ?? []), ...(p.dst_addr ?? [])],
  address_groups: (p) => [...(p.src_addr ?? []), ...(p.dst_addr ?? [])],
  services: (p) => p.service ?? [],
  service_groups: (p) => p.service ?? [],
  schedules: (p) => [p.schedule],
  feeds: (p) => p.threat_feeds ?? [],
  'profiles.ips': (p) => [p.ips_profile],
  'profiles.webfilter': (p) => [p.webfilter_profile],
  'profiles.dnsfilter': (p) => [p.dnsfilter_profile],
  'profiles.appctrl': (p) => [p.appctrl_profile],
}

// usedBy lists what references the entry name of the list at path, e.g.
// ['policy 14 "wifi-out"', 'address group "kids"']; [] = unused.
export function usedBy(path: string, name: string, cfg: Candidate): string[] {
  const out: string[] = []
  const fields = policyFields[path]
  if (fields) {
    for (const p of cfg.policies ?? []) {
      if (fields(p).includes(name)) out.push(`policy ${p.id} "${p.name}"`)
    }
  }
  if (path === 'addresses' || path === 'address_groups') {
    for (const f of cfg.nat?.port_forwards ?? []) if (f.source?.includes(name)) out.push(`port forward "${f.name}"`)
  }
  if (path === 'addresses') {
    for (const g of cfg.address_groups ?? []) if (g.members?.includes(name)) out.push(`address group "${g.name}"`)
  }
  if (path === 'services') {
    for (const g of cfg.service_groups ?? []) if (g.members?.includes(name)) out.push(`service group "${g.name}"`)
  }
  if (path === 'feeds') {
    for (const d of cfg.profiles?.dnsfilter ?? []) if (d.block_feeds?.includes(name)) out.push(`DNS filter profile "${d.name}"`)
  }
  return out
}
