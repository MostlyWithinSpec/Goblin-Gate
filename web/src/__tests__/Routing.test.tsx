import React from 'react'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'
import type { RoutingStatus } from '../api/routing'

vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true }) }
})
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/routing', () => ({ getRoutingStatus: vi.fn() }))

import * as api from '../api/objects'
import { getRoutingStatus } from '../api/routing'
import Routing from '../pages/network/Routing'

const cfg: Candidate = {
  addresses: [
    { name: 'srv01', type: 'host', value: '192.168.1.20' },
    { name: 'office', type: 'subnet', value: '10.0.0.0/24' },
  ],
  interfaces: [
    { name: 'wan', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '10.0.0.1/24', enabled: true },
  ],
  vlans: [{ name: 'guest', parent: 'lan', vlan_id: 20, mode: 'static', address: '10.0.20.1/24', enabled: true }],
}

const emptyStatus: RoutingStatus = { bgp_enabled: false, ospf_enabled: false, running: false, bgp: [], ospf: [], routes: [] }

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(getRoutingStatus).mockReset().mockResolvedValue(structuredClone(emptyStatus))
})

describe('Routing page', () => {
  it('links the wiki and shows both protocols off to begin with', async () => {
    wrap(<Routing />)
    expect(await screen.findByRole('heading', { name: 'Dynamic Routing' })).toBeInTheDocument()
    expect(screen.getByLabelText('Speak BGP with other routers')).not.toBeChecked()
    expect(screen.getByLabelText('Speak OSPF inside this network')).not.toBeChecked()
    // no status card while the running config has neither protocol on
    expect(screen.queryByRole('table', { name: 'BGP sessions table' })).toBeNull()
    expect(screen.queryByRole('table', { name: 'OSPF adjacencies table' })).toBeNull()
    const link = screen.getByRole('link', { name: 'Dynamic Routing' })
    expect(link.getAttribute('href')).toMatch('wiki/Dynamic-Routing')
  })

  it('saves BGP with a neighbour, and leaves the OSPF section alone', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak BGP with other routers'))
    fireEvent.change(screen.getByLabelText('Our AS number'), { target: { value: '64512' } })
    fireEvent.click(screen.getByLabelText('office'))
    fireEvent.click(screen.getByLabelText('lan'))
    fireEvent.click(screen.getByLabelText('Redistribute connected networks'))
    fireEvent.click(screen.getByRole('button', { name: '+ Add neighbour' }))
    fireEvent.change(screen.getByLabelText('Neighbour address of Neighbour 1'), { target: { value: '198.51.100.1' } })
    fireEvent.change(screen.getByLabelText('Remote AS of Neighbour 1'), { target: { value: '64513' } })
    fireEvent.change(screen.getByLabelText('Password of Neighbour 1'), { target: { value: 's3cret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('routing')
    expect(value).toMatchObject({
      bgp: {
        enabled: true,
        as: 64512,
        announce: ['office', 'lan'],
        redistribute_connected: true,
        neighbors: [{ address: '198.51.100.1', remote_as: 64513, enabled: true, password: 's3cret' }],
      },
      ospf: { enabled: false },
    })
    // the field names name the field first, then which neighbour it belongs to
    expect(screen.getByLabelText('Neighbour address of Neighbour 1')).toHaveValue('198.51.100.1')
    expect(screen.getByLabelText('Remote AS of Neighbour 1')).toHaveValue('64513')
    expect(screen.getByLabelText('Password of Neighbour 1')).toHaveValue('s3cret')
    expect(screen.getByLabelText('Description of Neighbour 1')).toBeEnabled()
  })

  it('keeps a stored neighbour password when the field is left empty', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({
      ...structuredClone(cfg),
      routing: {
        router_id: '10.0.0.1',
        bgp: { enabled: true, as: 64512, neighbors: [{ address: '198.51.100.1', remote_as: 64513, enabled: true, password: 'stored' }] },
        ospf: { enabled: false },
      },
    })
    wrap(<Routing />)
    await screen.findByDisplayValue('198.51.100.1')
    // A full admin is shown the stored password, so editing anything else cannot
    // drop it; a caller who may not see secrets gets "" and the placeholder says
    // the stored one is kept. Both cases go back unchanged when the field is
    // left as it was.
    const pw = screen.getByLabelText('Password of Neighbour 1') as HTMLInputElement
    expect(pw.value).toBe('stored')
    // the placeholder only speaks for a caller who was not shown the secret
    expect(pw).not.toHaveAttribute('placeholder', 'unchanged')
    fireEvent.change(screen.getByLabelText('Description of Neighbour 1'), { target: { value: 'uplink' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    expect(value).toMatchObject({ bgp: { neighbors: [{ address: '198.51.100.1', description: 'uplink', remote_as: 64513, password: 'stored' }] } })
  })

  it('sends no password for a caller who cannot see the stored one', async () => {
    // a read-only view of the config: the secret arrives blank
    vi.mocked(api.getCandidate).mockResolvedValue({
      ...structuredClone(cfg),
      routing: { bgp: { enabled: true, as: 64512, neighbors: [{ address: '198.51.100.1', remote_as: 64513, enabled: true, password: '' }] }, ospf: { enabled: false } },
    })
    wrap(<Routing />)
    await screen.findByDisplayValue('198.51.100.1')
    const pw = screen.getByLabelText('Password of Neighbour 1') as HTMLInputElement
    expect(pw.value).toBe('')
    expect(pw).toHaveAttribute('placeholder', 'unchanged')
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    expect((value as { bgp: { neighbors: { password?: string }[] } }).bgp.neighbors[0].password).toBeUndefined()
  })

  it('refuses to save a bad AS and shows the message under the field', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak BGP with other routers'))
    fireEvent.change(screen.getByLabelText('Our AS number'), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('must be an AS number')
    expect(alert.parentElement).toBe(screen.getByLabelText('Our AS number').closest('label'))
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('saves an OSPF area with an interface, cost and passive flag', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak OSPF inside this network'))
    fireEvent.click(screen.getByRole('button', { name: '+ Add area' }))
    fireEvent.change(screen.getByLabelText('Area ID in Area 1'), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add interface in Area 1' }))
    fireEvent.change(screen.getByLabelText('Interface 1 in Area 1'), { target: { value: 'lan' } })
    fireEvent.change(screen.getByLabelText('Cost 1 in Area 1'), { target: { value: '20' } })
    fireEvent.click(screen.getByLabelText('Passive 1 in Area 1'))
    fireEvent.click(screen.getByRole('button', { name: 'Save OSPF' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('routing')
    expect(value).toMatchObject({
      bgp: { enabled: false, as: 0 },
      ospf: { enabled: true, areas: [{ id: '0', interfaces: [{ name: 'lan', cost: 20, passive: true }] }] },
    })
  })

  it('shows an error under the interface row when an interface lands in two areas', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak OSPF inside this network'))
    fireEvent.click(screen.getByRole('button', { name: '+ Add area' }))
    fireEvent.click(screen.getByRole('button', { name: '+ Add interface in Area 1' }))
    fireEvent.change(screen.getByLabelText('Interface 1 in Area 1'), { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add area' }))
    fireEvent.change(screen.getByLabelText('Area ID in Area 2'), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add interface in Area 2' }))
    fireEvent.change(screen.getByLabelText('Interface 1 in Area 2'), { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save OSPF' }))
    await waitFor(() => expect(screen.getAllByRole('alert').length).toBeGreaterThan(0))
    expect(screen.getByText(/lan is in more than one area/)).toBeInTheDocument()
    expect(screen.getByText('area 0.0.0.0 is listed twice')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('adds an ordered prefix filter that the up button re-orders', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak BGP with other routers'))
    fireEvent.change(screen.getByLabelText('Our AS number'), { target: { value: '64512' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add neighbour' }))
    fireEvent.click(screen.getByRole('button', { name: '+ Add rule in Import filter 1' }))
    fireEvent.change(screen.getByLabelText('Prefix 1 in Import filter 1'), { target: { value: '10.0.0.0/8' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add rule in Import filter 1' }))
    fireEvent.change(screen.getByLabelText('Prefix 2 in Import filter 1'), { target: { value: '0.0.0.0/0' } })
    // the second rule moved up in front of the first, so a permit inside the
    // default stays meaningful
    fireEvent.click(screen.getByRole('button', { name: 'Move rule up 2 in Import filter 1' }))
    fireEvent.change(screen.getByLabelText('Neighbour address of Neighbour 1'), { target: { value: '198.51.100.1' } })
    fireEvent.change(screen.getByLabelText('Remote AS of Neighbour 1'), { target: { value: '64513' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, value] = lastPatch()
    expect(value).toMatchObject({
      bgp: { neighbors: [{ import: [{ action: 'permit', prefix: '0.0.0.0/0' }, { action: 'permit', prefix: '10.0.0.0/8' }] }] },
    })
  })

  it('rejects a prefix with host bits and names the field', async () => {
    wrap(<Routing />)
    fireEvent.click(await screen.findByLabelText('Speak BGP with other routers'))
    fireEvent.change(screen.getByLabelText('Our AS number'), { target: { value: '64512' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add neighbour' }))
    fireEvent.click(screen.getByRole('button', { name: '+ Add rule in Export filter 1' }))
    fireEvent.change(screen.getByLabelText('Prefix 1 in Export filter 1'), { target: { value: '10.0.0.5/8' } })
    fireEvent.change(screen.getByLabelText('Neighbour address of Neighbour 1'), { target: { value: '198.51.100.1' } })
    fireEvent.change(screen.getByLabelText('Remote AS of Neighbour 1'), { target: { value: '64513' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save BGP' }))
    expect(screen.getByText(/has host bits set/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('shows the BGP sessions with their badges, uptime and prefix counts', async () => {
    vi.mocked(getRoutingStatus).mockResolvedValue({
      ...structuredClone(emptyStatus),
      bgp_enabled: true,
      running: true,
      bgp: [
        { address: '198.51.100.1', family: 'ipv4', remote_as: 64513, description: 'uplink', state: 'Established', up: true, uptime_seconds: 3725, prefixes_received: 42, prefixes_sent: 3 },
        { address: '2001:db8::9', family: 'ipv6', remote_as: 64514, state: 'Active', up: false, uptime_seconds: 0, prefixes_received: 0, prefixes_sent: 0 },
      ],
    })
    wrap(<Routing />)
    const table = await screen.findByRole('table', { name: 'BGP sessions table' })
    expect(within(table).getByText('Established')).toBeInTheDocument()
    expect(within(table).getByText('uplink')).toBeInTheDocument()
    expect(within(table).getByText('1 h')).toBeInTheDocument()
    expect(within(table).getByText('42')).toBeInTheDocument()
    expect(within(table).getByText('3')).toBeInTheDocument()
    // a session that is not up shows its state and no uptime
    expect(within(table).getByText('Active')).toBeInTheDocument()
    expect(within(table).getAllByText('not up')).toHaveLength(1)
  })

  it('warns that FRR is not running yet while a protocol is enabled', async () => {
    vi.mocked(getRoutingStatus).mockResolvedValue({ ...structuredClone(emptyStatus), bgp_enabled: true, running: false })
    wrap(<Routing />)
    expect(await screen.findByText('FRR is enabled but not running yet: it starts after the next commit.')).toBeInTheDocument()
  })

  it('shows OSPF adjacencies with the state text when it is not formed', async () => {
    vi.mocked(getRoutingStatus).mockResolvedValue({
      ...structuredClone(emptyStatus),
      ospf_enabled: true,
      running: true,
      ospf: [
        { router_id: '10.0.0.2', address: '10.0.0.2', interface: 'eth1', state: 'Full/DR', full: true },
        { router_id: '10.0.0.3', address: '10.0.0.3', interface: 'eth2', state: '2-Way/DROther', full: false },
      ],
    })
    wrap(<Routing />)
    const table = await screen.findByRole('table', { name: 'OSPF adjacencies table' })
    expect(within(table).getByText('Full/DR')).toBeInTheDocument()
    expect(within(table).getByText('2-Way/DROther')).toBeInTheDocument()
    expect(within(table).getByText('eth1')).toBeInTheDocument()
  })

  it('lists learned routes and narrows them with the search box', async () => {
    vi.mocked(getRoutingStatus).mockResolvedValue({
      ...structuredClone(emptyStatus),
      bgp_enabled: true,
      running: true,
      routes: [
        { prefix: '0.0.0.0/0', protocol: 'bgp', selected: true, installed: true, distance: 20, metric: 0, next_hops: ['198.51.100.1'], interface: 'wan' },
        { prefix: '10.10.0.0/16', protocol: 'ospf', selected: true, installed: true, distance: 110, metric: 10, next_hops: ['10.0.0.2'], interface: 'lan' },
        { prefix: '10.20.0.0/16', protocol: 'ospf', selected: false, installed: false, distance: 110, metric: 30, next_hops: ['10.0.0.3'], interface: 'lan' },
      ],
    })
    wrap(<Routing />)
    const table = await screen.findByRole('table', { name: 'Learned routes' })
    expect(within(table).getAllByRole('row')).toHaveLength(4) // header + 3
    expect(within(table).getAllByText('best route')).toHaveLength(2)
    expect(within(table).getByText('not chosen')).toBeInTheDocument()
    // the cell holding the badges says which prefix they describe
    const marked = within(table).getAllByLabelText(/^route to /)
    expect(marked).toHaveLength(3)
    expect(marked[0]).toHaveTextContent('best route')

    fireEvent.change(screen.getByRole('searchbox', { name: 'Search routes' }), { target: { value: 'ospf' } })
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(3))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search routes' }), { target: { value: '198.51.100.1' } })
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(2))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search routes' }), { target: { value: 'nothing' } })
    await waitFor(() => expect(screen.getByText('No learned route matches your search.')).toBeInTheDocument())
    // the table is replaced by the message, not left with a header row
    expect(screen.queryByRole('table', { name: 'Learned routes' })).toBeNull()
  })

  it('says when the live status is not available here', async () => {
    vi.mocked(getRoutingStatus).mockRejectedValue(new Error('unavailable'))
    wrap(<Routing />)
    expect(await screen.findByText('The live routing status is not available on this box.')).toBeInTheDocument()
    // the forms still work
    expect(screen.getByLabelText('Speak BGP with other routers')).toBeEnabled()
  })

  it('hides the controls for a role that cannot write the network config', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({
      ...structuredClone(cfg),
      routing: { bgp: { enabled: true, as: 64512, neighbors: [{ address: '198.51.100.1', remote_as: 64513, enabled: true }] }, ospf: { enabled: false } },
    })
    vi.mocked((await import('../api/auth')).getMe).mockResolvedValue({
      user: 'net',
      role: 'netops',
      method: 'session',
      permissions: ['read', 'write:firewall'],
    } as never)
    wrap(<Routing />)
    await screen.findByRole('heading', { name: 'Dynamic Routing' })
    expect(screen.getByLabelText('Speak BGP with other routers')).toBeDisabled()
    expect(screen.queryByRole('button', { name: '+ Add neighbour' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save BGP' })).toBeNull()
    expect(screen.getByText('Your role cannot change these objects; you can still see them.')).toBeInTheDocument()
  })
})
