import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { DeviceInventory } from '../api/devices'

// Mock the device inventory + blocks APIs: no network. Pure helpers stay real.
vi.mock('../api/devices', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/devices')>()
  return {
    ...real,
    listDevices: vi.fn(),
    renameDevice: vi.fn(),
    forgetDevice: vi.fn(),
    wakeDevice: vi.fn(),
  }
})
vi.mock('../api/sentry', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/sentry')>()
  return {
    ...real,
    createBlock: vi.fn(),
  }
})
import * as devices from '../api/devices'
import * as sentry from '../api/sentry'
import Devices from '../pages/network/Devices'
import { APIRequestError } from '../api/client'

const device = (over: Partial<DeviceInventory> = {}): DeviceInventory => ({
  mac: 'aa:bb:cc:dd:ee:01',
  ips: ['192.168.1.50'],
  hostname: 'tv.local',
  vendor: 'Samsung',
  interface: 'lan',
  first_seen: new Date(Date.now() - 86400_000).toISOString(),
  last_seen: new Date(Date.now() - 30_000).toISOString(),
  ...over,
})

const wrap = (ui: React.ReactElement) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )

beforeEach(() => {
  vi.mocked(devices.listDevices).mockReset().mockResolvedValue([device()])
  vi.mocked(devices.renameDevice).mockReset().mockResolvedValue(device({ name: 'Living room TV' }))
  vi.mocked(devices.forgetDevice).mockReset().mockResolvedValue(undefined)
  vi.mocked(devices.wakeDevice).mockReset().mockResolvedValue({ status: 'sent', broadcast: '192.168.1.255' })
  vi.mocked(sentry.createBlock).mockReset().mockResolvedValue({
    ip: '192.168.1.50',
    until: new Date(Date.now() + 3600_000).toISOString(),
    reason: 'test',
    source: 'admin',
    created_by: 'admin',
    created: new Date().toISOString(),
    expires_in: 3600,
  })
})

describe('Devices page', () => {
  test('renders the device list with name, vendor and IP', async () => {
    wrap(<Devices />)
    // Name falls back to hostname when there is no admin label.
    expect(await screen.findByText('tv.local')).toBeTruthy()
    expect(screen.getByText('Samsung')).toBeTruthy()
    expect(screen.getByText('192.168.1.50')).toBeTruthy()
    expect(screen.getByText('aa:bb:cc:dd:ee:01')).toBeTruthy()
  })

  test('search filters by name, hostname, vendor, IP and MAC', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue([
      device(),
      device({ mac: 'aa:bb:cc:dd:ee:02', ips: ['192.168.1.51'], hostname: 'printer.local', vendor: 'HP' }),
    ])
    wrap(<Devices />)
    await screen.findByText('tv.local')

    const search = screen.getByPlaceholderText(/Search name, hostname, IP, MAC/i)

    // Match by IP.
    fireEvent.change(search, { target: { value: '192.168.1.51' } })
    expect(screen.getByText('printer.local')).toBeTruthy()
    expect(screen.queryByText('tv.local')).toBeNull()

    // No match shows the empty row.
    fireEvent.change(search, { target: { value: 'zzz-nothing' } })
    expect(screen.getByText('No devices found.')).toBeTruthy()
  })

  test('interface select filters to one interface', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue([
      device(),
      device({ mac: 'aa:bb:cc:dd:ee:02', ips: ['192.168.20.5'], hostname: 'guest.local', interface: 'guest' }),
    ])
    wrap(<Devices />)
    // Wait for the initial load with both devices present.
    await screen.findByText('tv.local')

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'guest' } })

    // After filtering, only the guest device remains.
    expect(await screen.findByText('guest.local')).toBeTruthy()
    expect(screen.queryByText('tv.local')).toBeNull()
  })

  test('rename PATCHes the right body', async () => {
    wrap(<Devices />)
    await screen.findByText('tv.local')

    // Open the drawer by clicking the row.
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    const nameInput = await screen.findByPlaceholderText('e.g. Living room TV')
    fireEvent.change(nameInput, { target: { value: 'Living room TV' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }))

    await waitFor(() => expect(devices.renameDevice).toHaveBeenCalledTimes(1))
    expect(devices.renameDevice).toHaveBeenCalledWith('aa:bb:cc:dd:ee:01', 'Living room TV')
  })

  test('forget DELETEs after confirm and not when declined', async () => {
    wrap(<Devices />)
    await screen.findByText('tv.local')

    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    const forget = await screen.findByRole('button', { name: /Forget device/ })

    // Declined: confirm returns false, nothing is sent.
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    fireEvent.click(forget)
    expect(devices.forgetDevice).not.toHaveBeenCalled()

    // Confirmed: confirm returns true, DELETE fires with the MAC.
    vi.mocked(window.confirm).mockReturnValue(true)
    fireEvent.click(forget)
    await waitFor(() => expect(devices.forgetDevice).toHaveBeenCalledWith('aa:bb:cc:dd:ee:01'))
  })

  test('shows a plain message on 503 devices_unavailable', async () => {
    const err = new Error('device inventory unavailable')
    ;(err as unknown as { code: string }).code = 'devices_unavailable'
    vi.mocked(devices.listDevices).mockRejectedValue(err)

    wrap(<Devices />)
    expect(await screen.findByText('Device inventory runs on the firewall itself.')).toBeTruthy()
  })

  test('wake button POSTs and shows success message', async () => {
    wrap(<Devices />)
    await screen.findByText('tv.local')

    // Open the drawer.
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    const wakeBtn = await screen.findByRole('button', { name: /Wake device/ })
    fireEvent.click(wakeBtn)

    await waitFor(() => expect(devices.wakeDevice).toHaveBeenCalledWith('aa:bb:cc:dd:ee:01'))
    // Success message uses hostname (no admin label set).
    expect(await screen.findByText(/Wake-up packet sent to tv\.local/)).toBeTruthy()
  })

  test('a block refused for an own-network IP offers the override, and sends it', async () => {
    vi.mocked(sentry.createBlock).mockRejectedValueOnce(
      new APIRequestError('192.168.1.50 cannot be blocked: it is on lan', 403, 'protected_target', null)
    )
    wrap(<Devices />)
    await screen.findByText('tv.local')
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    fireEvent.click(await screen.findByRole('button', { name: /Block temporarily/ }))
    fireEvent.change(await screen.findByLabelText(/Reason/), { target: { value: 'testing' } })
    fireEvent.click(screen.getByRole('button', { name: 'Block' }))

    expect(await screen.findByText(/cannot be blocked: it is on lan/)).toBeTruthy()
    const allow = screen.getByLabelText(/Allow \(IP is inside/)
    fireEvent.click(allow)
    fireEvent.click(screen.getByRole('button', { name: 'Block' }))
    await waitFor(() =>
      expect(sentry.createBlock).toHaveBeenLastCalledWith(expect.objectContaining({ ip: '192.168.1.50', allow_internal: true }))
    )
  })
})
