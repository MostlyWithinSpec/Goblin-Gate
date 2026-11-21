import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { DeviceInventory, DeviceList } from '../api/devices'

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
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn().mockResolvedValue({}) }
})
vi.mock('../api/radius', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/radius')>()
  return { ...real, getRadiusSessions: vi.fn().mockResolvedValue({ sessions: [] }), setDeviceVLAN: vi.fn() }
})
import * as objects from '../api/objects'
import * as radiusApi from '../api/radius'
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
  localStorage.clear()
  vi.mocked(devices.listDevices).mockReset().mockResolvedValue({ devices: [device()] })
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
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [
        device(),
        device({ mac: 'aa:bb:cc:dd:ee:02', ips: ['192.168.1.51'], hostname: 'printer.local', vendor: 'HP' }),
      ],
    })
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
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [
        device(),
        device({ mac: 'aa:bb:cc:dd:ee:02', ips: ['192.168.20.5'], hostname: 'guest.local', interface: 'guest' }),
      ],
    })
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

describe('Devices page: plugin annotations', () => {
  // A UniFi-style set: wired on a switch port, or on an AP's SSID.
  const wired = {
    connection: 'wired',
    switch: 'Switch-Office',
    port: 'gi1/0/5',
    link_mbps: '1000',
    network: 'LAN',
    vlan: '10',
  }
  const wifi = { connection: 'wifi', ap: 'AP-Lounge', ssid: 'Guest', band: '5 GHz' }
  const updated = { unifi: new Date(Date.now() - 90_000).toISOString() }

  test('annotation columns are hidden until switched on, then remembered', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [device({ annotations: { unifi: wired } })],
      annotations_updated: updated,
    })
    wrap(<Devices />)
    await screen.findByText('tv.local')
    // Off by default: the switch is nowhere in the table.
    expect(screen.queryByText('Switch / port')).toBeNull()

    const toggle = (label: RegExp) => within(screen.getByRole('menu')).getByLabelText(label)
    // openCols shows the picker if it is not already open. Checking a box leaves
    // the menu standing, so the second time round it only has to make sure of that.
    const openCols = () => {
      if (screen.queryByRole('menu')) return
      fireEvent.click(screen.getByRole('button', { name: /Columns/ }))
      expect(screen.getByRole('menu')).toBeTruthy()
    }
    openCols()
    // Switch/port joins up, the rest are named after their field.
    expect(screen.getByText('Switch / port')).toBeTruthy()
    expect(screen.getByText('Connection')).toBeTruthy()
    expect(screen.getByText('Link (Mbit/s)')).toBeTruthy()
    expect(screen.getAllByText('From unifi').length).toBeGreaterThan(0)

    fireEvent.click(within(screen.getByRole('menu')).getByLabelText(/^Switch \/ port/))
    fireEvent.click(within(screen.getByRole('menu')).getByLabelText(/^Link \(Mbit/))
    // The menu still says "Switch / port", so look for the new column heading.
    const headers = () => screen.getAllByRole('columnheader').map((h) => h.textContent ?? '')
    await waitFor(() => expect(headers()).toContain('Switch / port'))
    expect(headers()).toContain('Link (Mbit/s)')
    expect(screen.getByText('Switch-Office · port gi1/0/5')).toBeTruthy()
    expect(screen.getByText('1000')).toBeTruthy()
    // Remembered for this browser.
    expect(JSON.parse(localStorage.getItem('goblin.deviceColumns') ?? '[]')).toEqual(['switch port', 'link_mbps'])

    // Switch one off again and it goes: check the column heading, not the value
    // (the device list also shows a "1000 Mb/s" link speed elsewhere).
    openCols()
    fireEvent.click(toggle(/^Link \(Mbit/))
    await waitFor(() => expect(headers()).not.toContain('Link (Mbit/s)'))
    expect(JSON.parse(localStorage.getItem('goblin.deviceColumns') ?? '[]')).toEqual(['switch port'])
  })

  test('a column stays out of the picker once a fixed column has that title', async () => {
    // "interface" is already a column of its own: no duplicate "Interface".
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [device({ annotations: { unifi: { interface: 'LAN 1', connection: 'wired' } } })],
    })
    wrap(<Devices />)
    await screen.findByText('tv.local')
    fireEvent.click(screen.getByRole('button', { name: /Columns/ }))
    const boxes = (await screen.getByRole('menu')).querySelectorAll('input[type=checkbox]')
    expect(boxes.length).toBe(1)
    expect(within(screen.getByRole('menu')).getByText('Connection')).toBeTruthy()
  })

  test('sorting by an annotation column orders by its values and flips', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [
        device({ mac: 'aa:bb:cc:dd:ee:01', hostname: 'a.local', annotations: { unifi: { connection: 'wifi' } } }),
        device({ mac: 'aa:bb:cc:dd:ee:02', hostname: 'b.local', annotations: { unifi: { connection: 'wired' } } }),
      ],
    })
    wrap(<Devices />)
    await screen.findByText('a.local')
    fireEvent.click(screen.getByRole('button', { name: /Columns/ }))
    fireEvent.click(await within(screen.getByRole('menu')).findByLabelText(/^Connection/))

    const header = screen.getByRole('button', { name: 'Connection' })
    const rows = () => screen.getAllByRole('row').slice(1).map((r) => r.textContent ?? '')
    fireEvent.click(header)
    expect(rows()[0]).toContain('a.local')
    fireEvent.click(header)
    expect(rows()[0]).toContain('b.local')
  })

  test('search matches an annotation value', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [
        device({ annotations: { unifi: wifi } }),
        device({ mac: 'aa:bb:cc:dd:ee:02', hostname: 'laptop.local', annotations: { unifi: { ssid: 'Office' } } }),
      ],
    })
    wrap(<Devices />)
    await screen.findByText('tv.local')
    fireEvent.change(screen.getByPlaceholderText(/Search name, hostname, IP, MAC/i), { target: { value: 'guest' } })
    expect(screen.getByText('tv.local')).toBeTruthy()
    expect(screen.queryByText('laptop.local')).toBeNull()
  })

  test('the drawer lists every field per plugin with the last update', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [device({ annotations: { unifi: { ...wired, seat: 'desk 4' } } })],
      annotations_updated: updated,
    })
    wrap(<Devices />)
    await screen.findByText('tv.local')
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))

    const section = await screen.findByText('From unifi')
    const block = section.parentElement as HTMLElement
    const field = (label: string) => within(block).getByText(label).parentElement?.textContent ?? ''
    expect(field('Connection:')).toContain('wired')
    expect(field('Link (Mbit/s):')).toContain('1000')
    // A field the gate has never seen is still shown.
    expect(field('Seat:')).toContain('desk 4')
    expect(within(block).getByText(/Updated .* ago/)).toBeTruthy()
    // Read-only: the drawer's only form is the rename one.
    expect(screen.getByPlaceholderText('e.g. Living room TV')).toBeTruthy()
  })

  test('a plugin with only an update time still gets a section', async () => {
    vi.mocked(devices.listDevices).mockResolvedValue({
      devices: [device()],
      annotations_updated: { unifi: updated.unifi },
    })
    wrap(<Devices />)
    await screen.findByText('tv.local')
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    expect(await screen.findByText('From unifi')).toBeTruthy()
  })

  test('nothing annotation-shaped without a plugin', async () => {
    wrap(<Devices />)
    await screen.findByText('tv.local')
    expect(screen.queryByRole('button', { name: /Columns/ })).toBeNull()
    fireEvent.click(screen.getByText('aa:bb:cc:dd:ee:01'))
    await screen.findByPlaceholderText('e.g. Living room TV')
    expect(screen.queryByText(/^From /)).toBeNull()
  })

  test('a device in quarantine gets a badge and can be approved into a VLAN', async () => {
    vi.mocked(objects.getCandidate).mockResolvedValue({
      vlans: [
        { name: 'iot', parent: 'lan', vlan_id: 20, mode: 'static', enabled: true },
        { name: 'quarantine', parent: 'lan', vlan_id: 99, mode: 'static', enabled: true },
      ],
      radius: { enabled: true, unknown: 'quarantine', quarantine_vlan: 'quarantine' },
    } as never)
    vi.mocked(radiusApi.getRadiusSessions).mockResolvedValue({
      sessions: [{ mac: 'aa:bb:cc:dd:ee:01', method: 'mab', nas: '192.168.1.2', vlan: 'quarantine', started: '', updated: '' }],
    })
    vi.mocked(radiusApi.setDeviceVLAN).mockResolvedValue({
      device: device({ vlan: 'iot' }),
      disconnect: { mac: 'aabbccddee01', client: 'switch', nas: '192.168.1.2', result: 'acked' },
    })
    wrap(<Devices />)
    expect(await screen.findByText('Quarantine')).toBeTruthy()
    fireEvent.click(await screen.findByRole('button', { name: 'Approve tv.local: move it out of quarantine' }))
    expect(await screen.findByText(/Move tv\.local from quarantine to iot\?/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(radiusApi.setDeviceVLAN).toHaveBeenCalledWith('aa:bb:cc:dd:ee:01', 'iot'))
    expect(await screen.findByText('tv.local is now in iot.')).toBeTruthy()
  })
})
