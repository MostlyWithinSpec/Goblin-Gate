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
vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getSystemStatus: vi.fn() }
})
import * as api from '../api/objects'
import * as stats from '../api/stats'
import { getSystemStatus } from '../api/config'
import Addresses from '../pages/network/Addresses'
import AddressGroups from '../pages/network/AddressGroups'
import Services from '../pages/network/Services'
import Interfaces from '../pages/network/Interfaces'
import ThreatFeeds from '../pages/security/ThreatFeeds'
import DNSFilterProfiles, { UsedBy } from '../pages/security/DNSFilterProfiles'
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
  vi.mocked(getSystemStatus).mockReset().mockResolvedValue({ version: 'test', sqm: [{ interface: 'wan1', active: true, download_mbit: 943, upload_mbit: 94 }] })
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
    expect(screen.getByText(/IPv4 or IPv6 subnet/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'office' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: ' 10.0.0.0/24 ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['addresses', [cfg.addresses![0], { name: 'office', type: 'subnet', value: '10.0.0.0/24' }]])
  })

  test('shows server validation errors for its list', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({ ...structuredClone(cfg), address_groups: [] })
    vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: [{ path: 'addresses[0].name', message: 'in use by policy x' }, { path: 'policies[0]', message: 'other' }] })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete nas' }))
    expect(await screen.findByText(/in use by policy x/)).toBeInTheDocument()
    expect(screen.queryByText('other')).not.toBeInTheDocument()
    expect(lastPatch()).toEqual(['addresses', []])
  })

  test('refuses to delete an address that is still used', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({
      ...structuredClone(cfg),
      policies: [{ id: 14, name: 'kids-night', enabled: true, src_addr: ['nas'], action: 'deny' }],
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete nas' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('"nas" is still used by policy 14 "kids-night", address group "servers". Remove it there first.')
    expect(confirm).not.toHaveBeenCalled()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('asks before deleting and keeps the entry on cancel', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({ ...structuredClone(cfg), address_groups: [] })
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete nas' }))
    await waitFor(() => expect(window.confirm).toHaveBeenCalledWith('Delete "nas"?'))
    expect(api.patchSection).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'nas' })).toBeInTheDocument()
  })

  test('accepts IPv6 values for subnet, host and range', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'nas' })
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))

    // v6 subnet
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'v6subnet' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'subnet' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '2001:db8::/32' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['addresses', [cfg.addresses![0], { name: 'v6subnet', type: 'subnet', value: '2001:db8::/32' }]])

    // v6 host
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'v6host' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'host' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '2001:db8::1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledTimes(2))
    const [, list2] = lastPatch() as [string, api.Address[]]
    expect(list2).toContainEqual({ name: 'v6host', type: 'host', value: '2001:db8::1' })

    // v6 range
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'v6range' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'range' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '2001:db8::1-2001:db8::ff' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledTimes(3))
    const [, list3] = lastPatch() as [string, api.Address[]]
    expect(list3).toContainEqual({ name: 'v6range', type: 'range', value: '2001:db8::1-2001:db8::ff' })
  })

  test('rejects mixed-family ranges and invalid v6', async () => {
    wrap(<Addresses />)
    await screen.findByRole('button', { name: 'nas' })
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))

    // Mixed family range (v4-v6)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'mixed' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'range' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '192.168.1.1-2001:db8::1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/range like/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()

    // Invalid v6 subnet (too many groups)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'badv6' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'subnet' } })
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: '2001:db8::1:2:3:4:5:6:7/64' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/IPv4 or IPv6 subnet/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
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
    fireEvent.change(screen.getByLabelText((t) => t === 'Mode'), { target: { value: 'dhcp' } })
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

  test('IPv6 static mode: PATCH body has correct ipv6 block', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    // Set IPv6 mode to static
    fireEvent.change(screen.getByLabelText('IPv6 mode'), { target: { value: 'static' } })
    // Enter the v6 address
    fireEvent.change(screen.getByLabelText('IPv6 address'), { target: { value: '2001:db8::1/64' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('interfaces')
    const lan = (list as api.Interface[])[1]
    expect(lan.ipv6).toEqual({ mode: 'static', address: '2001:db8::1/64' })
  })

  test('IPv6 delegated mode: PATCH body has uplink and prefix_id', async () => {
    // Set wan1 to dhcp6 so it can be an uplink for delegated.
    const cfgWithDhcp6 = structuredClone(cfg)
    cfgWithDhcp6.interfaces![0].ipv6 = { mode: 'dhcp6' }
    vi.mocked(api.getCandidate).mockResolvedValue(cfgWithDhcp6)

    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    // Set IPv6 mode to delegated
    fireEvent.change(screen.getByLabelText('IPv6 mode'), { target: { value: 'delegated' } })
    // Select the uplink
    fireEvent.change(screen.getByLabelText('IPv6 uplink'), { target: { value: 'wan1' } })
    // Set prefix_id
    fireEvent.change(screen.getByLabelText('IPv6 prefix ID'), { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('interfaces')
    const lan = (list as api.Interface[])[1]
    expect(lan.ipv6).toEqual({ mode: 'delegated', uplink: 'wan1', prefix_id: 3 })
  })

  test('IPv6 dhcp6 on non-WAN is rejected by validation', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    // lan is not wan, so dhcp6 option should not be available.
    // Verify the select does not offer dhcp6 for a LAN interface.
    const modeSelect = screen.getByLabelText('IPv6 mode') as HTMLSelectElement
    const options = Array.from(modeSelect.options).map((o) => o.value)
    expect(options).not.toContain('dhcp6')
    expect(options).toContain('delegated')
  })

  test('IPv6 dhcp6 on WAN: pd_size in PATCH body', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'wan1' }))
    // wan is the role, so dhcp6 should be available
    fireEvent.change(screen.getByLabelText('IPv6 mode'), { target: { value: 'dhcp6' } })
    fireEvent.change(screen.getByLabelText('IPv6 PD size'), { target: { value: '56' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('interfaces')
    const wan1 = (list as api.Interface[])[0]
    expect(wan1.ipv6).toEqual({ mode: 'dhcp6', pd_size: 56 })
  })

  test('smart queue section appears for WANs only', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'wan1' }))
    expect(await screen.findByText('Smart queue (reduces lag when the line is busy)')).toBeInTheDocument()
    // Close and open a LAN: no smart queue section there.
    const close = screen.getByRole('button', { name: '' })
    fireEvent.click(close)
    await waitFor(() => expect(screen.queryByRole('button', { name: 'wan1' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'lan' }))
    await screen.findByText('Edit lan')
    expect(screen.queryByText('Smart queue (reduces lag when the line is busy)')).not.toBeInTheDocument()
  })

  test('saving an enabled smart queue PATCHes the sqm block', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'wan1' }))
    fireEvent.click(await screen.findByLabelText('Enable smart queue'))
    fireEvent.change(screen.getByLabelText('SQM download Mbit'), { target: { value: '943' } })
    fireEvent.change(screen.getByLabelText('SQM upload Mbit'), { target: { value: '94' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch()
    expect(path).toBe('interfaces')
    const wan1 = (list as api.Interface[])[0]
    expect(wan1.sqm).toEqual({ enabled: true, download_mbit: 943, upload_mbit: 94 })
  })

  test('disabling smart queue keeps the speeds in the saved body', async () => {
    const cfgWithSqm = structuredClone(cfg)
    cfgWithSqm.interfaces![0].sqm = { enabled: true, download_mbit: 500, upload_mbit: 50 }
    vi.mocked(api.getCandidate).mockResolvedValue(cfgWithSqm)

    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'wan1' }))
    const enable = await screen.findByLabelText('Enable smart queue')
    expect(enable).toBeChecked()
    fireEvent.click(enable)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, list] = lastPatch()
    const wan1 = (list as api.Interface[])[0]
    expect(wan1.sqm).toEqual({ enabled: false, download_mbit: 500, upload_mbit: 50 })
  })

  test('enabling smart queue with empty speeds shows validation errors', async () => {
    wrap(<Interfaces />)
    fireEvent.click(await screen.findByRole('button', { name: 'wan1' }))
    fireEvent.click(await screen.findByLabelText('Enable smart queue'))
    expect(screen.getByLabelText('SQM download Mbit')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    const msg = /about 90% of the measured speed/
    expect(await screen.findAllByText(msg)).toHaveLength(2)
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('SQM badge on an enabled WAN shows the live state', async () => {
    const cfgWithSqm = structuredClone(cfg)
    cfgWithSqm.interfaces![0].sqm = { enabled: true, download_mbit: 943, upload_mbit: 94 }
    vi.mocked(api.getCandidate).mockResolvedValue(cfgWithSqm)

    wrap(<Interfaces />)
    const row = (await screen.findByRole('button', { name: 'wan1' })).closest('tr')!
    expect(within(row).getByText('SQM').parentElement).toHaveAttribute('title', 'smart queue active')
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

  test('warns about profiles no policy applies', () => {
    render(<UsedBy used={{ family: ['guest'] }} names={['family', 'kids']} />)
    expect(screen.getByTestId('unused-profiles')).toHaveTextContent('Not applied anywhere: kids.')
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

describe('?edit= opens the drawer (global search links)', () => {
  test('addresses', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
    render(
      <MemoryRouter initialEntries={['/network/addresses?edit=nas']}>
        <Addresses />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Edit nas')).toBeInTheDocument()
  })
})
