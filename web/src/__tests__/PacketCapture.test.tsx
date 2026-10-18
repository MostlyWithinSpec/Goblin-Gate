import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/capture', async (orig) => {
  const real = await orig<typeof import('../api/capture')>()
  return { ...real, startCapture: vi.fn(), getCapture: vi.fn(), stopCapture: vi.fn() }
})
vi.mock('../api/objects', async (orig) => {
  const real = await orig<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn() }
})
import * as captureApi from '../api/capture'
import type { CapturePacket, CaptureStatus } from '../api/capture'
import * as objectsApi from '../api/objects'
import PacketCapture from '../pages/log/PacketCapture'

// jsdom has no EventSource; this fake records instances so tests can emit
// the named events (packet / dropped / done) the server sends.
type Listener = (e: MessageEvent) => void
class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  listeners = new Map<string, Set<Listener>>()
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(name: string, l: Listener) {
    if (!this.listeners.has(name)) this.listeners.set(name, new Set())
    this.listeners.get(name)!.add(l)
  }
  removeEventListener(name: string, l: Listener) {
    this.listeners.get(name)?.delete(l)
  }
  close() {}
  emit(name: string, data: unknown) {
    for (const l of this.listeners.get(name) ?? []) {
      l({ data: JSON.stringify(data) } as MessageEvent)
    }
  }
}

const status = (s: Partial<CaptureStatus>): CaptureStatus => ({
  id: 'cap-1',
  state: 'running',
  interface: 'lan',
  filter: 'ip and host 192.168.1.50',
  packets: 0,
  bytes: 0,
  started: '2026-10-17T10:00:00Z',
  ...s,
})
const pkt = (n: number): CapturePacket => ({
  n,
  time: new Date(1760000000000 + n).toISOString(),
  src: '192.168.1.50',
  dst: '8.8.8.8',
  protocol: 'udp',
  length: 120,
  info: `DNS query ${n}`,
})

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.mocked(objectsApi.getCandidate).mockResolvedValue({
    interfaces: [{ name: 'lan', device: 'eth1', role: 'lan', mode: 'static', enabled: true }],
    vlans: [{ name: 'guest.vl10', parent: 'eth1', vlan_id: 10, mode: 'none', enabled: true }],
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const open = () =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={['/log/capture']}>
        <Routes>
          <Route path="/log/capture" element={<PacketCapture />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )

describe('Packet capture', () => {
  test('start sends the form values to POST /api/v1/capture', async () => {
    vi.mocked(captureApi.startCapture).mockResolvedValue(status({}))
    open()
    await screen.findByText('lan')
    fireEvent.change(screen.getByLabelText('Interface'), { target: { value: 'lan' } })
    fireEvent.change(screen.getByLabelText('Host (IP)'), { target: { value: '192.168.1.50' } })
    fireEvent.change(screen.getByLabelText('Port'), { target: { value: '53' } })
    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'udp' } })
    fireEvent.change(screen.getByLabelText('Max packets'), { target: { value: '2000' } })
    fireEvent.click(screen.getByRole('button', { name: /Start capture/ }))

    expect(await screen.findByText('Capturing…')).toBeInTheDocument()
    expect(captureApi.startCapture).toHaveBeenCalledWith({
      interface: 'lan',
      host: '192.168.1.50',
      port: 53,
      protocol: 'udp',
      max_packets: 2000,
      max_seconds: 60,
    })
    expect(FakeEventSource.instances[0].url).toBe('/api/v1/capture/cap-1/live')
  })

  test('live packets appear in the table and are capped at 500 rows', async () => {
    vi.mocked(captureApi.startCapture).mockResolvedValue(status({}))
    vi.mocked(captureApi.getCapture).mockResolvedValue(status({ packets: 1, bytes: 120 }))
    open()
    await screen.findByText('lan')
    fireEvent.change(screen.getByLabelText('Interface'), { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: /Start capture/ }))
    await screen.findByText('Capturing…')

    const es = FakeEventSource.instances[0]
    for (let i = 1; i <= 600; i++) es.emit('packet', pkt(i))
    await waitFor(() => expect(screen.getByText('DNS query 600')).toBeInTheDocument())
    // newest first, capped at 500: oldest visible is #101
    expect(screen.getByText('DNS query 101')).toBeInTheDocument()
    expect(screen.queryByText('DNS query 100')).not.toBeInTheDocument()

    es.emit('dropped', { count: 7 })
    es.emit('dropped', { count: 3 })
    await waitFor(() => expect(screen.getByText('10 dropped')).toBeInTheDocument())
  })

  test('stop calls DELETE and the done state offers the pcap download', async () => {
    vi.mocked(captureApi.startCapture).mockResolvedValue(status({}))
    vi.mocked(captureApi.getCapture).mockResolvedValue(status({ packets: 5, bytes: 600 }))
    vi.mocked(captureApi.stopCapture).mockResolvedValue(undefined)
    open()
    await screen.findByText('lan')
    fireEvent.change(screen.getByLabelText('Interface'), { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: /Start capture/ }))
    await screen.findByText('Capturing…')

    const es = FakeEventSource.instances[0]
    es.emit('done', status({ state: 'done', finished: '2026-10-17T10:01:00Z' }))
    vi.mocked(captureApi.getCapture).mockResolvedValue(status({ state: 'done', packets: 5, bytes: 600, finished: '2026-10-17T10:01:00Z' }))

    fireEvent.click(screen.getByRole('button', { name: /Stop/ }))
    await waitFor(() => expect(captureApi.stopCapture).toHaveBeenCalledWith('cap-1'))
    await waitFor(() => expect(screen.getByText('Done')).toBeInTheDocument())
    const link = screen.getByRole('link', { name: /Download pcap/ })
    expect(link).toHaveAttribute('href', '/api/v1/capture/cap-1/pcap')
    expect(screen.getByText(/Wireshark/)).toBeInTheDocument()
  })

  test('a 409 busy error shows the already-running message', async () => {
    vi.mocked(captureApi.startCapture).mockRejectedValue({
      name: 'APIRequestError',
      message: 'A capture is already running',
      status: 409,
      code: 'busy',
      details: { id: 'cap-9' },
    })
    open()
    await screen.findByText('lan')
    fireEvent.change(screen.getByLabelText('Interface'), { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: /Start capture/ }))

    expect(await screen.findByText(/A capture is already running/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View it' })).toBeInTheDocument()
  })
})
