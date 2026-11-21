import React from 'react'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'
import type { SitesStatus } from '../api/sites'
import { APIRequestError } from '../api/client'

vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true }) }
})
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/sites', () => ({
  getSitesStatus: vi.fn(),
  getInvitations: vi.fn(),
  createInvitation: vi.fn(),
  cancelInvitation: vi.fn(),
  checkJoin: vi.fn(),
  joinSite: vi.fn(),
  acceptReply: vi.fn(),
}))

import * as api from '../api/objects'
import * as sites from '../api/sites'
import { getMe } from '../api/auth'
import VpnSites from '../pages/VpnSites'

const cfg: Candidate = {
  interfaces: [
    { name: 'wan', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true },
  ],
  vpn: {
    sites: {
      enabled: true,
      address: '10.254.1.1',
      endpoint: 'home.example.net',
      networks: ['lan'],
      remotes: [
        { name: 'cabin', enabled: true, public_key: 'YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=', endpoint: 'cabin.example.net:51830', listen_port: 51830, address: '10.254.2.2', networks: ['192.168.50.0/24'] },
        { name: 'office', enabled: true, via: 'cabin', address: '10.254.3.3', networks: ['192.168.60.0/24'] },
        { name: 'flat', enabled: false, public_key: 'xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=', listen_port: 51831, address: '10.254.4.4' },
      ],
    },
  },
}

const status: SitesStatus = {
  enabled: true,
  address: '10.254.1.1',
  sites: [
    { name: 'cabin', enabled: true, address: '10.254.2.2', endpoint: '198.51.100.7:51830', latest_handshake: 1, handshake_age: 42, rx_bytes: 2048, tx_bytes: 1048576, reachable: true, rtt_ms: 1.5 },
    { name: 'office', enabled: true, via: 'cabin', address: '10.254.3.3', latest_handshake: 0, handshake_age: -1, rx_bytes: 0, tx_bytes: 0, reachable: false },
    { name: 'flat', enabled: false, address: '10.254.4.4', latest_handshake: 0, handshake_age: -1, rx_bytes: 0, tx_bytes: 0 },
  ],
}

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
  vi.clearAllMocks()
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(sites.getSitesStatus).mockReset().mockResolvedValue(structuredClone(status))
  vi.mocked(sites.getInvitations).mockReset().mockResolvedValue({ invitations: [] })
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
})

describe('VPN → Sites', () => {
  it('lists the sites with their status', async () => {
    wrap(<VpnSites />)
    const table = await screen.findByRole('table', { name: 'Sites table' })
    expect(await within(table).findByLabelText('status of cabin')).toHaveTextContent('Reachable · 1.5 ms')
    expect(within(table).getByText('42 s ago')).toBeInTheDocument()
    expect(within(table).getByText('↓ 2.0 KB ↑ 1.0 MB')).toBeInTheDocument()
    expect(within(table).getByText('198.51.100.7:51830')).toBeInTheDocument()
    expect(within(table).getByText('through cabin')).toBeInTheDocument()
    expect(within(table).getByLabelText('status of office')).toHaveTextContent('No answer')
    expect(within(table).getByLabelText('status of flat')).toHaveTextContent('Off')
    expect(screen.getByText('10.254.1.1')).toBeInTheDocument()
  })

  it('saves this gate settings and keeps the sites', async () => {
    wrap(<VpnSites />)
    const endpoint = await screen.findByLabelText(/Public name or address of this gate/)
    await waitFor(() => expect(endpoint).toHaveValue('home.example.net'))
    fireEvent.change(endpoint, { target: { value: 'gw.example.org' } })
    fireEvent.click(screen.getByLabelText('SSH'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('vpn.sites')
    expect(value).toMatchObject({ enabled: true, address: '10.254.1.1', endpoint: 'gw.example.org', networks: ['lan'], allow_access: ['ssh'] })
    expect((value as { remotes: unknown[] }).remotes).toHaveLength(3)
  })

  it('rejects a bad endpoint before saving', async () => {
    wrap(<VpnSites />)
    const endpoint = await screen.findByLabelText(/Public name or address of this gate/)
    fireEvent.change(endpoint, { target: { value: 'not a host' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Want a DNS name or an IP address/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('invites a site: shows the code, copies it, accepts the reply', async () => {
    vi.mocked(sites.createInvitation).mockResolvedValue({ id: 'abc', code: 'GGSITE1-xyz', site: 'lake', port: 51832, expires: new Date(Date.now() + 60 * 60000).toISOString(), sites_enabled: false })
    vi.mocked(sites.acceptReply).mockResolvedValue({ site: { name: 'lake', enabled: true } })
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Invite a site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Invite a site' })
    const name = within(dialog).getByLabelText(/Name for the other site/)
    fireEvent.change(name, { target: { value: 'lan' } })
    expect(within(dialog).getByText(/already a site or an interface/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Make the code' })).toBeDisabled()
    fireEvent.change(name, { target: { value: 'lake' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Make the code' }))
    expect(await within(dialog).findByLabelText('Invitation code')).toHaveValue('GGSITE1-xyz')
    expect(sites.createInvitation).toHaveBeenCalledWith('lake', 60)
    expect(within(dialog).getByText(/expires in 60 min/)).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy the invitation code' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('GGSITE1-xyz'))
    fireEvent.change(within(dialog).getByLabelText('Reply code'), { target: { value: 'GGREPLY1-abc' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Accept a reply' }))
    expect(await within(dialog).findByText(/lake was added. Commit on both gates/)).toBeInTheDocument()
    expect(sites.acceptReply).toHaveBeenCalledWith('GGREPLY1-abc')
  })

  it('shows why a reply was refused', async () => {
    vi.mocked(sites.createInvitation).mockResolvedValue({ id: 'abc', code: 'GGSITE1-xyz', site: 'lake', port: 51832, expires: new Date(Date.now() + 60 * 60000).toISOString(), sites_enabled: true })
    vi.mocked(sites.acceptReply).mockRejectedValue(new APIRequestError('192.168.1.0/24 overlaps interface lan', 409, 'overlap', [{ code: 'overlap', network: '192.168.1.0/24', message: '192.168.1.0/24 overlaps interface lan (192.168.1.0/24) on this gate: renumber one of them first' }]))
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Invite a site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Invite a site' })
    fireEvent.change(within(dialog).getByLabelText(/Name for the other site/), { target: { value: 'lake' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Make the code' }))
    expect(await within(dialog).findByText(/Sites were turned on/)).toBeInTheDocument()
    fireEvent.change(within(dialog).getByLabelText('Reply code'), { target: { value: 'GGREPLY1-abc' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Accept a reply' }))
    expect(await within(dialog).findByText(/renumber one of them first/)).toBeInTheDocument()
  })

  it('joins a site: reviews the networks, then shows the reply code', async () => {
    vi.mocked(sites.checkJoin).mockResolvedValue({ site: 'home', endpoint: 'home.example.net:51830', address: '10.254.9.9', networks: ['192.168.9.0/24'], public_key: 'k', problems: [] })
    vi.mocked(sites.joinSite).mockResolvedValue({ site: { name: 'home2', enabled: true }, reply: 'GGREPLY1-back' })
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Join a site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Join a site' })
    fireEvent.change(within(dialog).getByLabelText('Invitation code'), { target: { value: 'GGSITE1-abc' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check the code' }))
    expect(await within(dialog).findByTestId('offer-networks')).toHaveTextContent('192.168.9.0/24')
    expect(within(dialog).getByText('home.example.net:51830')).toBeInTheDocument()
    const name = within(dialog).getByLabelText(/Name for the other site on this gate/)
    expect(name).toHaveValue('home')
    fireEvent.change(name, { target: { value: 'home2' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Join' }))
    expect(await within(dialog).findByLabelText('Reply code')).toHaveValue('GGREPLY1-back')
    expect(sites.joinSite).toHaveBeenCalledWith('GGSITE1-abc', 'home2')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy the reply code' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('GGREPLY1-back'))
  })

  it('refuses to join a code whose networks overlap', async () => {
    vi.mocked(sites.checkJoin).mockResolvedValue({
      site: 'other', address: '10.254.9.9', networks: ['192.168.1.0/24'], public_key: 'k',
      problems: [{ code: 'overlap', network: '192.168.1.0/24', message: '192.168.1.0/24 overlaps interface lan (192.168.1.0/24) on this gate: renumber one of them first' }],
    })
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Join a site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Join a site' })
    fireEvent.change(within(dialog).getByLabelText('Invitation code'), { target: { value: 'GGSITE1-abc' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check the code' }))
    expect(await within(dialog).findByText(/overlaps interface lan/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Join' })).toBeDisabled()
    expect(sites.joinSite).not.toHaveBeenCalled()
  })

  it('shows a bad code', async () => {
    vi.mocked(sites.checkJoin).mockRejectedValue(new Error('the invitation expired at 2026-11-20T12:00:00Z: make a new one'))
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Join a site' }))
    const dialog = await screen.findByRole('dialog', { name: 'Join a site' })
    fireEvent.change(within(dialog).getByLabelText('Invitation code'), { target: { value: 'GGSITE1-old' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Check the code' }))
    expect(await within(dialog).findByText(/invitation expired/)).toBeInTheDocument()
  })

  it('edits a site and removes one with the sites behind it', async () => {
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit cabin' }))
    const drawer = await screen.findByRole('dialog', { name: 'Site cabin' })
    expect(within(drawer).getByLabelText(/^Name/)).toBeDisabled()
    fireEvent.change(within(drawer).getByLabelText(/Keepalive/), { target: { value: '9999' } })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }))
    expect(await within(drawer).findByText('Want 0-3600 seconds.')).toBeInTheDocument()
    fireEvent.change(within(drawer).getByLabelText(/Keepalive/), { target: { value: '10' } })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const remotes = (lastPatch()[1] as { remotes: { name: string; keepalive?: number; public_key?: string }[] }).remotes
    expect(remotes[0]).toMatchObject({ name: 'cabin', keepalive: 10, public_key: 'YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=' })

    fireEvent.click(await screen.findByRole('button', { name: 'Remove cabin' }))
    const confirm = await screen.findByRole('dialog', { name: 'Remove the site' })
    expect(within(confirm).getByText(/Sites reached through it are removed too/)).toBeInTheDocument()
    fireEvent.click(within(confirm).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect((lastPatch()[1] as { remotes: { name: string }[] }).remotes.map((r) => r.name)).toEqual(['flat']))
  })

  it('adds a site reached through another one', async () => {
    wrap(<VpnSites />)
    fireEvent.click(await screen.findByRole('button', { name: 'Add a site reached through another' }))
    const drawer = await screen.findByRole('dialog', { name: 'Site reached through another site' })
    fireEvent.change(within(drawer).getByLabelText(/^Name/), { target: { value: 'shop' } })
    fireEvent.change(within(drawer).getByLabelText(/Tunnel address of the other gate/), { target: { value: '10.254.5.5' } })
    fireEvent.change(within(drawer).getByLabelText(/Networks of the other site/), { target: { value: '192.168.80.0/24\n192.168.81.0/24' } })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const remotes = (lastPatch()[1] as { remotes: object[] }).remotes
    expect(remotes[3]).toEqual({ name: 'shop', enabled: true, via: 'cabin', address: '10.254.5.5', networks: ['192.168.80.0/24', '192.168.81.0/24'] })
  })

  it('lists open invitations and cancels one', async () => {
    vi.mocked(sites.getInvitations).mockResolvedValue({ invitations: [{ id: 'i1', site: 'lake', port: 51832, expires: new Date(Date.now() + 30 * 60000).toISOString(), created: new Date().toISOString() }] })
    vi.mocked(sites.cancelInvitation).mockResolvedValue({ status: 'ok' })
    wrap(<VpnSites />)
    expect(await screen.findByText(/lake · expires in 30 min/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel the invitation for lake' }))
    await waitFor(() => expect(sites.cancelInvitation).toHaveBeenCalledWith('i1'))
  })

  it('hides pairing from users without full admin', async () => {
    vi.mocked(getMe).mockResolvedValueOnce({ user: 'ops', role: 'network', method: 'session', full_admin: false, permissions: ['read', 'write:network'] } as never)
    wrap(<VpnSites />)
    expect(await screen.findByText(/Inviting and joining sites needs a full admin/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Invite a site' })).not.toBeInTheDocument()
  })
})
