import { describe, expect, it } from 'vitest'
import { buildSettings, buildSite, formatAge, siteBadge, siteFormOf, validCIDR, validHostPort, validateSite, zoneNames } from '../pages/vpn/sitesLogic'
import { objectsOf } from '../api/policy'
import type { Site, SiteStatus } from '../api/sites'

const cabin: Site = { name: 'cabin', enabled: true, public_key: 'k', endpoint: 'cabin.example.net:51830', listen_port: 51830, address: '10.254.2.2', networks: ['192.168.50.0/24'] }
const st = (p: Partial<SiteStatus>): SiteStatus => ({ name: 'cabin', enabled: true, address: '10.254.2.2', latest_handshake: 0, handshake_age: -1, rx_bytes: 0, tx_bytes: 0, ...p })

describe('sitesLogic', () => {
  it('keeps the address and remotes when saving the settings', () => {
    const out = buildSettings({ enabled: true, endpoint: ' gw.example.net ', networks: ['lan'], https: true, ssh: false, announce: false }, { enabled: false, address: '10.254.1.1', remotes: [cabin] })
    expect(out).toEqual({ enabled: true, address: '10.254.1.1', remotes: [cabin], endpoint: 'gw.example.net', networks: ['lan'], allow_access: ['https'], announce: undefined })
  })

  it('keeps a paired site\'s key and port, and strips them from a via site', () => {
    const f = siteFormOf(cabin)
    expect(buildSite({ ...f, keepalive: '10' }, cabin)).toMatchObject({ public_key: 'k', listen_port: 51830, keepalive: 10, endpoint: 'cabin.example.net:51830' })
    const via = buildSite({ ...f, via: 'hub', address: '10.254.9.9', networks: '10.9.0.0/16, 10.10.0.0/16' }, cabin)
    expect(via).toEqual({ name: 'cabin', enabled: true, address: '10.254.9.9', via: 'hub', networks: ['10.9.0.0/16', '10.10.0.0/16'] })
  })

  it('validates like the server', () => {
    expect(validCIDR('192.168.50.0/24')).toBe(true)
    expect(validCIDR('192.168.50.1/24')).toBe(false)
    expect(validCIDR('0.0.0.0/0')).toBe(false)
    expect(validHostPort('cabin.example.net:51830')).toBe(true)
    expect(validHostPort('[2001:db8::1]:51830')).toBe(true)
    expect(validHostPort('cabin.example.net')).toBe(false)
    const empty = { ...siteFormOf(), name: 'x' }
    expect(validateSite({ ...empty, name: '1abc' }, [], [], true).name).toBe('name')
    expect(validateSite({ ...empty, name: 'lan' }, [], ['lan'], true).name).toBe('taken')
    expect(validateSite({ ...empty, via: 'cabin', address: 'nope', networks: '' }, [cabin], [], true)).toMatchObject({ address: 'address', networks: '' })
    expect(validateSite({ ...empty, via: 'ghost', address: '10.254.3.3', networks: '10.3.0.0/16' }, [cabin], [], true).via).toBe('via')
  })

  it('badges', () => {
    expect(siteBadge(st({ reachable: true, handshake_age: 4 }), true).kind).toBe('ok')
    expect(siteBadge(st({ reachable: false, handshake_age: 4 }), true)).toEqual({ kind: 'warn', key: 'noAnswer' })
    expect(siteBadge(st({ reachable: false, handshake_age: 600 }), true)).toEqual({ kind: 'bad', key: 'noHandshake' })
    expect(siteBadge(undefined, true).key).toBe('notRunning')
    expect(siteBadge(st({ reachable: true }), false).key).toBe('disabled')
    expect(formatAge(42)).toBe('42 s')
    expect(formatAge(600)).toBe('10 min')
    expect(formatAge(-1)).toBe('')
  })

  it('offers direct sites as policy zones', () => {
    const sites = { enabled: true, remotes: [cabin, { name: 'office', enabled: true, via: 'cabin' }, { name: 'flat', enabled: false }] }
    expect(zoneNames(sites)).toEqual(['cabin'])
    expect(zoneNames({ ...sites, enabled: false })).toEqual([])
    expect(objectsOf({ interfaces: [{ name: 'lan' }], vpn: { sites } }).interfaces).toEqual(['lan', 'cabin'])
  })
})
