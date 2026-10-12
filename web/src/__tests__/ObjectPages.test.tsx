import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

// Mock the API modules: no network. The pure helpers stay real.
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getDevices: vi.fn(),
    getFeedStatus: vi.fn(),
    refreshFeed: vi.fn(),
    lookupFeeds: vi.fn(),
    getWebCategories: vi.fn(),
    lookupWebCategory: vi.fn(),
    getApps: vi.fn(),
  }
})
vi.mock('../api/stats', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/stats')>()
  return { ...real, listLogs: vi.fn() }
})
import * as api from '../api/objects'
import * as stats from '../api/stats'
import Addresses from '../pages/network/Addresses'
import AddressGroups from '../pages/network/AddressGroups'
import Services from '../pages/network/Services'
import Interfaces from '../pages/network/Interfaces'
import ThreatFeeds from '../pages/security/ThreatFeeds'
import DNSFilterProfiles from '../pages/security/DNSFilterProfiles'
import WebFilterProfiles from '../pages/security/WebFilterProfiles'
import AppControl from '../pages/security/AppControl'

const cfg: Candidate = {
  interfaces: [
    { name: 'wan1', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true, allow_access: ['ping', 'https'] },
  ],
  vlans: [{ name: 'guest', parent: 'lan', vlan_id: 20, mode: 'static', address: '192.168.20.1/24', enabled: true }],
  addresses: [{ name: 'nas', type: 'host', value: '192.168.1.5' }],
  address_groups: [{ name: 'servers', members: ['nas'] }],
  services: [{ name: 'https', protocol: 'tcp', ports: '443' }],
  policies: [
    { id: 1, name: 'guest-out', enabled: true, src_intf: ['guest'], dst_intf: ['wan1'], action: 'accept', appctrl_profile: 'kids', webfilter_profile: 'family' },
    { id: 2, name: 'lan-out', enabled: true, src_intf: ['all'], dst_intf: ['wan1'], action: 'accept', appctrl_profile: 'office' },
  ],
  feeds: [
    { name: 'spamhaus', type: 'ip', url: 'https://www.spamhaus.org/drop/drop.txt', enabled: true, block_wan: true },
    { name: 'ads', type: 'domain', url: 'https://example.org/ads.txt', enabled: true },
  ],
  profiles: {
    dnsfilter: [{ name: 'noads', block_feeds: ['ads'] }],
    webfilter: [{ name: 'family', categories: { adult: 'block', gambling: 'warn' } }],
    appctrl: [
      { name: 'kids', groups: { Gaming: 'block' }, apps: { steam: 'allow' } },
      { name: 'office', groups: {} },
    ],
  },
}

const apps: api.App[] = [
  { id: 'steam', name: 'Steam', group: 'Gaming', domains: ['steampowered.com'] },
  { id: 'roblox', name: 'Roblox', group: 'Gaming', domains: ['roblox.com'] },
  { id: 'netflix', name: 'Netflix', group: 'Streaming', domains: ['netflix.com'] },
]

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}
const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getDevices).mockResolvedValue([{ name: 'eth0', up: true }, { name: 'eth1', up: true }])
  vi.mocked(api.getFeedStatus).mockResolvedValue([{ name: 'spamhaus', type: 'ip', url: '', enabled: true, count: 1234, fetched_at: new Date().toISOString() }])
  vi.mocked(api.getWebCategories).mockResolvedValue({ categories: ['adult', 'gambling', 'news'], entries: 5000 })
  vi.mocked(api.getApps).mockResolvedValue(apps)
  vi.mocked(stats.listLogs).mockResolvedValue({
    events: [{ id: 7, time: 1760000000, kind: 'appctrl', action: 'deny', src_ip: '192.168.20.9', app: 'roblox', category: 'Gaming', message: 'roblox.com' }],
    next_cursor: 0,
  })
})

describe('Addresses', () => {
  test('renders the list from the candidate', async () => {
    wrap(<Addresses />)
    expect(await screen.findByRole('button', { name: 'nas' })).toBeInTheDocument()
    expect(screen.getByText('192.168.1.5')).toBeInTheDocument()
  })

  test('validates and PATCHes the whole list', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'nas' })
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'nas' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '10.0.0.0/33' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Name already used')).toBeInTheDocument()
    expect(screen.getByText(/IPv4 subnet/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'office' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: ' 10.0.0.0/24 ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['addresses', [cfg.addresses![0], { name: 'office', type: 'subnet', value: '10.0.0.0/24' }]])
  })

  test('shows server validation errors for its list', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: [{ path: 'addresses[0].name', message: 'in use by policy x' }, { path: 'policies[0]', message: 'other' }] })
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete nas' }))
    expect(await screen.findByText(/in use by policy x/)).toBeInTheDocument()
    expect(screen.queryByText('other')).not.toBeInTheDocument()
    expect(lastPatch()).toEqual(['addresses', []])
  })
})

describe('Groups and services', () => {
  test('address group needs members', async () => {
    wrap(<AddressGroups />)
    await screen.findByRole('button', { name: 'servers' })
    fireEvent.click(screen.getByRole('button', { name: /New address group/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'g2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Pick at least one address')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('nas'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()).toEqual(['address_groups', [cfg.address_groups![0], { name: 'g2', members: ['nas'] }]]))
  })

  test('icmp services drop their ports; bad ports are rejected', async () => {
    wrap(<Services />)
    await screen.findByRole('button', { name: 'https' })
    fireEvent.click(screen.getByRole('button', { name: /New service/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'web' } })
    fireEvent.change(screen.getByLabelText('Ports'), { target: { value: '80,70000' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Use ports and ranges/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'icmp' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()?.[1]).toContainEqual({ name: 'web', protocol: 'icmp' }))
  })
})

describe('Interfaces', () => {
  test('lists interfaces and VLANs', async () => {
    wrap(<Interfaces />)
    expect(await screen.findByRole('button', { name: 'lan' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'guest' })).toBeInTheDocument()
    expect(screen.getByText('192.168.1.1/24')).toBeInTheDocument()
  })

  test('switching to dhcp removes the static address on save', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'dhcp' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('interfaces')
    expect((list as api.Interface[])[1]).toEqual({ name: 'lan', device: 'eth1', role: 'lan', mode: 'dhcp', enabled: true, allow_access: ['ping', 'https'] })
  })

  test('static address must be a CIDR and gateway inside it', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    fireEvent.change(screen.getByLabelText('Gateway'), { target: { value: '10.0.0.1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/inside the interface subnet/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })
})

describe('ThreatFeeds', () => {
  test('joins config with live status', async () => {
    wrap(<ThreatFeeds />)
    expect(await screen.findByRole('button', { name: 'spamhaus' })).toBeInTheDocument()
    await screen.findByText('1234')
    const ads = screen.getByRole('button', { name: 'ads' }).closest('tr')!
    expect(within(ads).getByText('not applied')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Refresh spamhaus' })).toBeInTheDocument()
  })

  test('validates refresh interval and URL', async () => {
    wrap(<ThreatFeeds />)
    await screen.findByRole('button', { name: 'spamhaus' })
    fireEvent.click(screen.getByRole('button', { name: /New feed/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'x' } })
    fireEvent.change(screen.getByLabelText('URL'), { target: { value: 'ftp://x' } })
    fireEvent.change(screen.getByLabelText('Refresh minutes'), { target: { value: '5' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/http:\/\/ or https:\/\//)).toBeInTheDocument()
    expect(screen.getByText(/15 minutes/)).toBeInTheDocument()
  })

  test('lookup shows the listing feeds', async () => {
    vi.mocked(api.lookupFeeds).mockResolvedValue({ query: '1.2.3.4', feeds: ['spamhaus'] })
    wrap(<ThreatFeeds />)
    fireEvent.change(await screen.findByLabelText('Lookup'), { target: { value: '1.2.3.4' } })
    fireEvent.click(screen.getByRole('button', { name: 'Look up' }))
    expect(await screen.findByTestId('lookup-result')).toHaveTextContent('listed by spamhaus')
  })
})

describe('DNS filter', () => {
  test('offers only domain feeds and rejects bad domains', async () => {
    wrap(<DNSFilterProfiles />)
    fireEvent.click(await screen.findByRole('button', { name: 'noads' }))
    expect(screen.getByLabelText('ads')).toBeChecked()
    expect(screen.queryByLabelText('spamhaus')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Always block'), { target: { value: 'tracker.example.com\nnot_a domain' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Not a domain: not_a/)).toBeInTheDocument()
  })
})

describe('Web filter', () => {
  test('sets category actions; allow is not stored', async () => {
    wrap(<WebFilterProfiles />)
    fireEvent.click(await screen.findByRole('button', { name: 'family' }))
    fireEvent.change(screen.getByLabelText('Action for gambling'), { target: { value: 'allow' } })
    fireEvent.change(screen.getByLabelText('Action for news'), { target: { value: 'monitor' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()).toEqual(['profiles.webfilter', [{ name: 'family', categories: { adult: 'block', news: 'monitor' } }]]))
  })

  test('shows which interfaces use a profile', async () => {
    wrap(<WebFilterProfiles />)
    expect(await screen.findByTestId('used-by')).toHaveTextContent('family: guest')
  })
})

describe('Application control', () => {
  test('resolves app actions from group and override, and PATCHes', async () => {
    wrap(<AppControl />)
    fireEvent.click(await screen.findByRole('button', { name: 'kids' }))
    // Steam overrides the blocked Gaming group, Roblox follows it.
    // the badge is the row's first <span> (the select repeats the action names)
    const badge = (name: string) => screen.getByLabelText(`Override for ${name}`).closest('tr')!.querySelector('span')
    await waitFor(() => expect(badge('Steam')).toHaveTextContent('allow'))
    expect(badge('Roblox')).toHaveTextContent('block')
    expect(badge('Netflix')).toHaveTextContent('allow')
    fireEvent.change(screen.getByLabelText('Override for Steam'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Action for group Streaming'), { target: { value: 'monitor' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['profiles.appctrl', [{ name: 'kids', groups: { Gaming: 'block', Streaming: 'monitor' } }, { name: 'office', groups: {} }]])
  })

  test('first policy wins per interface; events table', async () => {
    wrap(<AppControl />)
    // guest is taken by policy 1 (kids); "all" in policy 2 gives office the rest.
    expect(await screen.findByTestId('used-by')).toHaveTextContent('kids: guest · office: lan')
    const events = await screen.findByTestId('app-events')
    expect(within(events).getByText('Roblox')).toBeInTheDocument()
    expect(within(events).getByText('blocked')).toBeInTheDocument()
    expect(stats.listLogs).toHaveBeenCalledWith({ kind: 'appctrl', limit: 50 })
  })
})
