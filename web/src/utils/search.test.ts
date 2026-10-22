import { describe, expect, it } from 'vitest'
import type { Candidate } from '../api/objects'
import { buildSearchIndex, score, search } from './search'

const cfg: Candidate = {
  policies: [{ id: 7, name: 'kids-bedtime', enabled: true, action: 'deny' }],
  addresses: [
    { name: 'nas', type: 'host', value: '192.168.1.5' },
    { name: 'printer', type: 'host', value: '192.168.1.9' },
  ],
  services: [{ name: 'plex', protocol: 'tcp', ports: '32400' }],
  vlans: [{ name: 'iot', parent: 'lan', vlan_id: 30, mode: 'static', enabled: true }],
  nat: { port_forwards: [{ name: 'plex-in' }] },
  dhcp: [{ interface: 'lan', static_leases: [{ mac: 'aa:bb:cc:00:00:01', ip: '192.168.1.50', hostname: 'tv' }] }],
}

describe('buildSearchIndex', () => {
  const index = buildSearchIndex(cfg)
  const find = (id: string) => index.find((i) => i.id === id)

  it('links objects to their edit drawer', () => {
    expect(find('Policy:7')?.path).toBe('/policy?edit=7')
    expect(find('Address:nas')?.path).toBe('/network/addresses?edit=nas')
    expect(find('Port forward:plex-in')?.path).toBe('/network/port-forwards?edit=plex-in')
    expect(find('VLAN:iot')?.path).toBe('/network/interfaces')
    expect(find('Lease:aa:bb:cc:00:00:01')?.name).toBe('tv')
  })

  it('includes the pages', () => {
    expect(find('page:/network/dynamic-dns')?.name).toBe('Dynamic DNS')
  })
})

describe('search', () => {
  const index = buildSearchIndex(cfg)

  it('ranks exact and prefix matches first', () => {
    const r = search(index, 'plex')
    expect(r[0].id).toBe('Service:plex')
    expect(r.map((i) => i.id)).toContain('Port forward:plex-in')
  })

  it('finds objects by their value', () => {
    expect(search(index, '192.168.1.9')[0].id).toBe('Address:printer')
  })

  it('finds pages by their words', () => {
    expect(search(index, 'ddns')[0].id).toBe('page:/network/dynamic-dns')
  })

  it('returns nothing for an empty query or no match', () => {
    expect(search(index, '  ')).toEqual([])
    expect(search(index, 'zzzz')).toEqual([])
  })

  it('scores', () => {
    expect(score('nas', 'nas')).toBe(100)
    expect(score('nas-backup', 'nas')).toBe(80)
    expect(score('home nas', 'nas')).toBe(70)
    expect(score('mynas', 'nas')).toBe(50)
    expect(score('network attached storage', 'nas')).toBe(20)
    expect(score('printer', 'nas')).toBe(0)
  })
})
