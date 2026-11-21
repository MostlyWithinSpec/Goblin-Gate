// Which scope may change each config section (task 150). This mirrors
// sectionScopes and adminSystemKeys in internal/api/scopes.go: the server is
// the authority and answers 403 for anything else; this only decides what the
// forms offer. Keep the two lists in step.
const sectionScopes: Record<string, string> = {
  addresses: 'write:firewall',
  address_groups: 'write:firewall',
  services: 'write:firewall',
  service_groups: 'write:firewall',
  policies: 'write:firewall',
  schedules: 'write:firewall',
  nat: 'write:firewall',
  profiles: 'write:firewall',
  feeds: 'write:firewall',
  geoip: 'write:firewall',
  upnp: 'write:firewall',
  custom_rules: 'write:firewall',
  crowdsec: 'write:firewall',
  reverse_proxy: 'write:firewall',
  internal_ca: 'write:firewall',
  ips: 'write:firewall',
  ups: 'write:system',

  interfaces: 'write:network',
  vlans: 'write:network',
  dhcp: 'write:network',
  dns: 'write:network',
  gateways: 'write:network',
  gateway_groups: 'write:network',
  routes: 'write:network',
  default_gateway: 'write:network',
  vpn: 'write:network',
  ddns: 'write:network',
  routing: 'write:network',

  system: 'write:system',
  notifications: 'write:system',
  log_export: 'write:system',
  flow_export: 'write:system',

  // root, accounts, code and whole-box changes stay with full admins
  schema_version: '',
  admins: '',
  roles: '',
  sso: '',
  plugins: '',
  git_backup: '',
}

// adminSystemKeys: system settings only full admins may change.
const adminSystemKeys = ['ssh_keys', 'update_url', 'update_channel']

// scopeForSection returns the scope a PATCH of the config path needs: '' means
// full admin only (an unknown section is admin-only too, like the server).
export function scopeForSection(path: string): string {
  const i = path.search(/[.[]/)
  const top = i < 0 ? path : path.slice(0, i)
  const sc = sectionScopes[top]
  if (sc === undefined) return ''
  if (top === 'system') {
    let key = path.slice(top.length).replace(/^[.]/, '')
    const j = key.search(/[.[]/)
    if (j >= 0) key = key.slice(0, j)
    if (!key || adminSystemKeys.includes(key)) return ''
  }
  return sc
}
