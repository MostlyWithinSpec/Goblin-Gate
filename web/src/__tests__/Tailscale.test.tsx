import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/tailscale', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/tailscale')>()
  return {
    ...real,
    getTailscaleStatus: vi.fn(),
    loginTailscale: vi.fn(),
    logoutTailscale: vi.fn(),
    patchTailscaleSettings: vi.fn(),
  }
})
vi.mock('../api/network', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/network')>()
  return { ...real, getNetworkConfig: vi.fn() }
})
import * as ts from '../api/tailscale'
import * as net from '../api/network'
import Tailscale from '../pages/Tailscale'

const wrap = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <Tailscale />
      </MemoryRouter>
    </QueryClientProvider>
  )

beforeEach(() => {
  vi.mocked(ts.getTailscaleStatus).mockReset().mockResolvedValue({
    available: true,
    backend_state: 'Running',
    hostname: 'gw',
    ips: ['100.64.0.1'],
    // the live status has CIDRs; the config holds interface names
    advertised_routes: ['192.168.1.0/24'],
    peers: [],
  })
  vi.mocked(ts.patchTailscaleSettings).mockReset().mockResolvedValue(undefined)
  vi.mocked(net.getNetworkConfig).mockReset().mockResolvedValue({
    interfaces: [{ name: 'lan', mode: 'static', address: '192.168.1.1/24' } as never],
    vpn: {
      tailscale: {
        enabled: true,
        hostname: 'gw',
        advertise_routes: ['lan'],
        accept_dns: true,
        login_server: 'https://hs.example.com',
        allow_access: ['ssh'],
      },
    },
  })
})

describe('Tailscale page', () => {
  test('a running node shows as Connected', async () => {
    wrap()
    expect(await screen.findByText('Connected')).toBeInTheDocument()
  })

  test('settings come from the config and saving keeps what was stored', async () => {
    wrap()
    await screen.findByText('Connected')
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    const drawer = await screen.findByRole('dialog', { name: 'Tailscale Settings' })
    expect(within(drawer).getByLabelText('Accept DNS')).toBeChecked()
    expect(within(drawer).getByDisplayValue('https://hs.example.com')).toBeInTheDocument()
    expect(within(drawer).getByText('lan')).toBeInTheDocument()
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save Settings' }))
    await waitFor(() => expect(ts.patchTailscaleSettings).toHaveBeenCalled())
    expect(vi.mocked(ts.patchTailscaleSettings).mock.calls[0][0]).toEqual({
      enabled: true,
      hostname: 'gw',
      advertise_routes: ['lan'],
      accept_dns: true,
      login_server: 'https://hs.example.com',
      allow_access: ['ssh'],
    })
    expect(await screen.findByText(/Tailscale settings saved/)).toBeInTheDocument()
  })

  test('the status refresh does not reset what is being typed', async () => {
    wrap()
    await screen.findByText('Connected')
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    const drawer = await screen.findByRole('dialog', { name: 'Tailscale Settings' })
    const host = within(drawer).getByDisplayValue('gw')
    fireEvent.change(host, { target: { value: 'gw-new' } })
    // a new status arrives (the page polls every 5 s)
    vi.mocked(ts.getTailscaleStatus).mockResolvedValue({ available: true, backend_state: 'Running', hostname: 'gw', peers: [] })
    await new Promise((r) => setTimeout(r, 50))
    expect(within(drawer).getByDisplayValue('gw-new')).toBeInTheDocument()
  })

  test('a failed save stays open and says why', async () => {
    vi.mocked(ts.patchTailscaleSettings).mockRejectedValue(new Error('forbidden'))
    wrap()
    await screen.findByText('Connected')
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }))
    const drawer = await screen.findByRole('dialog', { name: 'Tailscale Settings' })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Save Settings' }))
    expect(await within(drawer).findByText('Could not save: forbidden')).toBeInTheDocument()
  })
})
