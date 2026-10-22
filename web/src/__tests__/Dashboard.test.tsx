import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/stats', async (orig) => {
  const real = await orig<typeof import('../api/stats')>()
  return {
    ...real,
    getDashboards: vi.fn(),
    saveDashboards: vi.fn(),
    statsQuery: vi.fn(),
    getSystemStats: vi.fn(),
    getFeedStats: vi.fn(),
    getPolicyStats: vi.fn(),
  }
})
// ECharts needs a canvas; render a stub showing the option's bar labels.
vi.mock('../pages/dashboard/Chart', () => ({
  default: ({ option }: { option: { yAxis?: { data?: string[] } } }) => <div data-testid="chart">{(option.yAxis?.data ?? []).join(',')}</div>,
}))
vi.mock('../api/objects', async (orig) => {
  const real = await orig<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn() }
})
vi.mock('../api/gateways', () => ({
  getGatewayStatus: vi.fn(),
  getWANStatus: vi.fn(),
  getWireGuardClientPublicKey: vi.fn(),
  setWireGuardClientPrivateKey: vi.fn(),
}))
import * as api from '../api/stats'
import * as gatewaysApi from '../api/gateways'
import * as objectsApi from '../api/objects'
import Dashboard from '../pages/Dashboard'

const renderPage = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <Dashboard />
      </MemoryRouter>
    </QueryClientProvider>
  )

beforeEach(() => {
  vi.mocked(api.getDashboards).mockResolvedValue({ version: 1, dashboards: [] })
  vi.mocked(api.saveDashboards).mockReset().mockResolvedValue({ status: 'saved' })
  vi.mocked(api.statsQuery).mockResolvedValue({ columns: ['src_ip', 'bytes'], rows: [['192.168.1.20', 4096]] })
  vi.mocked(api.getSystemStats).mockResolvedValue({ cpu_percent: 12.5, mem_total_bytes: 2 * 1024 ** 3, mem_used_bytes: 1024 ** 3, uptime_seconds: 90061, sessions: 42 })
  vi.mocked(api.getFeedStats).mockResolvedValue({ feeds: [] })
  vi.mocked(api.getPolicyStats).mockResolvedValue({ policies: [{ id: 1, packets: 120, bytes: 9000 }] })
  vi.mocked(objectsApi.getCandidate).mockResolvedValue({ policies: [{ id: 1, name: 'lan-to-internet' } as never] })
  vi.mocked(gatewaysApi.getWANStatus).mockResolvedValue([{ name: 'wan1', device: 'ens192', up: true, addresses: ['24.101.68.119/24'], gateway: '24.101.68.1' }])
  vi.mocked(gatewaysApi.getGatewayStatus).mockResolvedValue({
    enabled: true,
    gateways: [
      { name: 'wan1', interface: 'eth0', state: 'up', loss_percent: 0, latency_ms: 12 },
      { name: 'wan2', interface: 'eth1', state: 'down', loss_percent: 100, latency_ms: 0 },
      { name: 'wan3', interface: 'eth2', state: 'unknown', loss_percent: 0, latency_ms: 0 },
    ],
    targets: [],
    default: { name: 'default', active: ['wan1'], mode: 'gateways' },
  })
})

describe('Dashboard', () => {
  test('shows the default widgets with real data', async () => {
    renderPage()
    expect(await screen.findByTestId('widget-system')).toBeInTheDocument()
    expect(await screen.findByText('12.5%')).toBeInTheDocument()
    expect(screen.getByText('1d 1h 1m')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
    expect(await screen.findByText('No threat feed hits since the last commit.')).toBeInTheDocument()
    expect(await screen.findByText('#1')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByTestId('chart').some((c) => c.textContent === '192.168.1.20')).toBe(true))
    expect(api.statsQuery).toHaveBeenCalledWith(expect.objectContaining({ group_by: ['src_ip'], metric: 'bytes', from: '-1h' }))
  })

  test('shows WAN status with up/down/unknown badges', async () => {
    renderPage()
    const tile = await screen.findByTestId('widget-gateways')
    expect(await within(tile).findByText('wan1')).toBeInTheDocument()
    expect(within(tile).getByText('up')).toBeInTheDocument()
    expect(within(tile).getByText('wan2')).toBeInTheDocument()
    expect(within(tile).getByText('down')).toBeInTheDocument()
    expect(within(tile).getByText('wan3')).toBeInTheDocument()
    expect(within(tile).getByText('unknown')).toBeInTheDocument()
    expect(gatewaysApi.getGatewayStatus).toHaveBeenCalled()
  })

  test('the policy leaderboard names the policies', async () => {
    renderPage()
    expect(await screen.findByText('#1')).toBeInTheDocument()
    expect(await screen.findByText(/lan-to-internet/)).toBeInTheDocument()
  })

  test('a single WAN without gateways shows its link and next hop', async () => {
    vi.mocked(gatewaysApi.getGatewayStatus).mockResolvedValue({ enabled: false, gateways: [], targets: [], default: { name: 'default', active: [], mode: 'default' } })
    renderPage()
    const tile = await screen.findByTestId('widget-gateways')
    expect(await within(tile).findByText(/via 24\.101\.68\.1/)).toBeInTheDocument()
    expect(within(tile).getByText('up')).toBeInTheDocument()
    expect(within(tile).queryByText(/No gateways/)).toBeNull()
  })

  test('changing the time range re-queries, and changes are saved', async () => {
    renderPage()
    const select = await screen.findByLabelText('Top talkers time range')
    fireEvent.change(select, { target: { value: '24h' } })
    await waitFor(() => expect(api.statsQuery).toHaveBeenCalledWith(expect.objectContaining({ group_by: ['src_ip'], from: '-24h' })))
    await waitFor(() => expect(api.saveDashboards).toHaveBeenCalled(), { timeout: 3000 })
    const calls = vi.mocked(api.saveDashboards).mock.calls
    const saved = calls[calls.length - 1][0]
    expect(saved.dashboards[0].widgets.find((w) => w.type === 'top-talkers')!.range).toBe('24h')
  })

  test('remove and add widgets', async () => {
    renderPage()
    fireEvent.click(await screen.findByLabelText('Remove System'))
    await waitFor(() => expect(screen.queryByTestId('widget-system')).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /Add widget/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'System' }))
    expect(await screen.findByTestId('widget-system')).toBeInTheDocument()
  })

  test('uses saved dashboards', async () => {
    vi.mocked(api.getDashboards).mockResolvedValue({
      version: 1,
      dashboards: [{ id: 'a', name: 'Kids', widgets: [{ id: 'w1', type: 'policy-hits', title: 'Policy hit leaderboard', range: '1h', layout: { x: 0, y: 0, w: 4, h: 4 } }] }],
    })
    renderPage()
    expect(await screen.findByRole('tab', { name: 'Kids' })).toBeInTheDocument()
    expect(screen.queryByTestId('widget-system')).not.toBeInTheDocument()
    expect(await screen.findByTestId('widget-policy-hits')).toBeInTheDocument()
  })
})
