import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/flows', async (orig) => {
  const real = await orig<typeof import('../api/flows')>()
  return { ...real, listFlows: vi.fn(), listFlowDevices: vi.fn() }
})
import * as flowsApi from '../api/flows'
import type { Flow, FlowDevice } from '../api/flows'
import Live from '../pages/log/Live'
import WidgetView from '../pages/dashboard/WidgetView'

const devices: FlowDevice[] = [
  { ip: '192.168.1.50', name: 'nas', rx_bps: 2_400_000, tx_bps: 300_000, flows: 12 },
  { ip: '192.168.1.7', rx_bps: 512_000, tx_bps: 64_000, flows: 3 },
]

const flow = (over: Partial<Flow> = {}): Flow => ({
  protocol: 'tcp',
  src: '192.168.1.50',
  src_port: 51234,
  dst: '93.184.216.34',
  dst_port: 443,
  state: 'ESTABLISHED',
  bytes_out: 1_000_000,
  bytes_in: 5_000_000,
  packets: 900,
  rate_out_bps: 2_400_000,
  rate_in_bps: 300_000,
  age_seconds: 12,
  ...over,
})

const open = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <Live />
      </MemoryRouter>
    </QueryClientProvider>,
  )

beforeEach(() => {
  vi.mocked(flowsApi.listFlowDevices).mockResolvedValue(devices)
  vi.mocked(flowsApi.listFlows).mockResolvedValue([flow()])
})

describe('Live page', () => {
  test('renders top devices with formatted rates and a sparkline', async () => {
    open()
    const row = await screen.findByTestId('device-192.168.1.50')
    expect(within(row).getByText('nas')).toBeTruthy()
    expect(within(row).getByTestId('down-192.168.1.50').textContent).toBe('2.40 Mbit/s')
    expect(screen.getByTestId('device-192.168.1.7')).toBeTruthy()
    expect(screen.getAllByTestId('sparkline').length).toBe(2)
  })

  test('selecting a device loads its flows with ?ip= and renders the table', async () => {
    open()
    fireEvent.click(await screen.findByTestId('device-192.168.1.50'))
    const remote = await screen.findByText('93.184.216.34:443')
    expect(remote).toBeTruthy()
    expect(screen.getByText('ESTABLISHED')).toBeTruthy()
    // device is src, so download = rate_out_bps
    expect(screen.getByTestId('flow-down-93.184.216.34').textContent).toBe('2.40 Mbit/s')
    expect(flowsApi.listFlows).toHaveBeenCalledWith('192.168.1.50')

    // the filter box hides non-matching rows
    fireEvent.change(screen.getByLabelText('Filter flows'), { target: { value: 'udp' } })
    expect(screen.queryByText('93.184.216.34:443')).toBeNull()
  })

  test('a 503 flows_unavailable shows the plain message', async () => {
    const err = new Error('flows unavailable') as Error & { code?: string }
    err.code = 'flows_unavailable'
    vi.mocked(flowsApi.listFlowDevices).mockRejectedValue(err)
    open()
    expect(await screen.findByText('Live flows are read on the firewall itself.')).toBeTruthy()
  })
})

describe('Top talkers (live) widget', () => {
  test('renders the top devices with formatted rates', async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter>
          <WidgetView
            widget={{ id: 'w1', type: 'live-top-talkers', title: 'Top talkers (live)', range: '1h', layout: { x: 0, y: 0, w: 4, h: 4 } }}
            onChange={() => {}}
            onRemove={() => {}}
          />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const row = await screen.findByTestId('talker-192.168.1.50')
    expect(within(row).getByText('nas')).toBeTruthy()
    expect(within(row).getByTestId('talker-down-192.168.1.50').textContent).toBe('2.40 Mbit/s')
  })
})
