// Tests for System → UPS (task 113): saving must never wipe the stored NUT
// password, the status card must show every power state, and the dashboard
// widget must show the badge, the charge bar and the runtime.
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate, UPSSettings, UPSStatus } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn(), getUPSStatus: vi.fn() }
})

import * as api from '../api/objects'
import UPS, { buildUps, formOf, runtimeText, validateUps, type UPSForm } from '../pages/system/UPS'
import WidgetView from '../pages/dashboard/WidgetView'
import type { Widget } from '../api/stats'

// The page and the widget poll, so retry and refetch are off to keep the tests
// deterministic.
const wrap = (ui: React.ReactElement) => {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, refetchInterval: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

const form = (over: Partial<UPSForm> = {}): UPSForm => ({
  enabled: true,
  mode: 'remote',
  host: '192.168.1.10',
  port: '3493',
  name: 'ups',
  username: 'monitor',
  password: '',
  percent: '20',
  runtime: '',
  hasSecret: true,
  ...over,
})

const status = (over: Partial<UPSStatus> = {}): UPSStatus => ({
  enabled: true,
  mode: 'usb',
  connected: true,
  state: 'online',
  charge: 100,
  runtime_seconds: 900,
  load: 42,
  model: 'CP1500PFCLCD',
  last_update: new Date().toISOString(),
  ...over,
})

const candidate = (ups?: UPSSettings): Candidate => ({ ups })

const lastPatch = (): [string, unknown] => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

const card = () => screen.getByTestId('ups-status')

// The card exists while the first reading is still in flight, so a test waits
// for the reading itself rather than for the card.
const settled = async () => {
  await waitFor(() => expect(within(card()).queryByText('Loading…')).toBeNull())
}

const openRemote = async () => {
  fireEvent.change(screen.getByLabelText('UPS mode'), { target: { value: 'remote' } })
  await waitFor(() => expect(screen.getByLabelText('NUT server')).toBeTruthy())
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(candidate())
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getUPSStatus).mockReset().mockResolvedValue(status())
})

describe('buildUps', () => {
  test('a password the caller cannot see is left out of the PATCH body', () => {
    const built = buildUps(form(), { enabled: true, mode: 'remote', host: '10.0.0.5', password: '' })
    expect('password' in built).toBe(false)
    expect(built).toEqual({
      enabled: true,
      mode: 'remote',
      host: '192.168.1.10',
      port: 3493,
      name: 'ups',
      username: 'monitor',
      shutdown_percent: 20,
    })
  })

  test('a password the caller can see is sent back unchanged when not retyped', () => {
    const built = buildUps(form(), { enabled: true, mode: 'remote', host: 'x', password: 'adminpw' })
    expect(built.password).toBe('adminpw')
  })

  test('a retyped password wins over the stored one', () => {
    const built = buildUps(form({ password: ' fresh ' }), { enabled: true, mode: 'remote', host: 'x', password: 'old' })
    expect(built.password).toBe('fresh')
  })

  test('a USB UPS sends no remote fields and no password at all', () => {
    const built = buildUps(
      form({ mode: 'usb', hasSecret: false, percent: '', runtime: '' }),
      { enabled: true, mode: 'remote', host: 'x', password: 'stored' },
    )
    expect(built).toEqual({ enabled: true, mode: 'usb' })
  })

  test('an empty port falls back to 3493 and empty thresholds are dropped', () => {
    const built = buildUps(form({ port: '', percent: '', runtime: '300' }), undefined)
    expect(built.port).toBe(3493)
    expect('shutdown_percent' in built).toBe(false)
    expect(built.shutdown_runtime).toBe(300)
  })

  test('formOf reads the stored settings and starts with a blank password', () => {
    const f = formOf({
      enabled: true, mode: 'remote', host: 'nuthost', port: 1234, name: 'eaton',
      username: 'mon', password: 'sekret', shutdown_runtime: 120,
    })
    expect(f).toMatchObject({
      enabled: true, mode: 'remote', host: 'nuthost', port: '1234', name: 'eaton',
      username: 'mon', password: '', runtime: '120', hasSecret: true,
    })
  })

  test('formOf of nothing stored is disabled, on USB, with the default port', () => {
    expect(formOf(undefined)).toEqual({
      enabled: false, mode: 'usb', host: '', port: '3493', name: '', username: '',
      password: '', percent: '', runtime: '', hasSecret: false,
    })
  })
})

describe('validateUps', () => {
  test('a remote UPS needs host, name, user and a password', () => {
    const e = validateUps(form({ host: '', name: '', username: '', password: '', hasSecret: false }))
    expect(Object.keys(e).sort()).toEqual(['host', 'name', 'password', 'username'])
  })

  test('a stored password counts as supplied', () => {
    expect(validateUps(form()).password).toBeUndefined()
  })

  test('a USB UPS needs nothing beyond the enable switch', () => {
    expect(validateUps(form({ mode: 'usb', host: '', name: '', username: '', hasSecret: false }))).toEqual({})
  })

  test('a percent threshold outside the server range is rejected, 0 and empty are fine', () => {
    expect(validateUps(form({ percent: '95' })).percent).toBeTruthy()
    expect(validateUps(form({ percent: '3' })).percent).toBeTruthy()
    expect(validateUps(form({ percent: '0' }))).toEqual({})
    expect(validateUps(form({ percent: '' }))).toEqual({})
  })

  test('a runtime threshold outside the server range is rejected, 0 and empty are fine', () => {
    expect(validateUps(form({ runtime: '30' })).runtime).toBeTruthy()
    expect(validateUps(form({ runtime: '7200' })).runtime).toBeTruthy()
    expect(validateUps(form({ runtime: '0' }))).toEqual({})
    expect(validateUps(form({ runtime: '' }))).toEqual({})
  })

  test('a bad port is rejected', () => {
    expect(validateUps(form({ port: '70000' })).port).toBeTruthy()
    expect(validateUps(form({ port: '' })).port).toBeUndefined()
  })
})

describe('runtimeText', () => {
  test('renders the battery runtime left as minutes or hours', () => {
    expect(runtimeText(900)).toBe('about 15 min')
    expect(runtimeText(60)).toBe('about 1 min')
    expect(runtimeText(7500)).toBe('about 2 h 5 min')
    expect(runtimeText(20)).toBe('less than a minute')
    expect(runtimeText(undefined)).toBe('—')
    expect(runtimeText(0)).toBe('—')
  })
})

describe('UPS page', () => {
  test('on mains: green badge, charge bar and runtime', async () => {
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('On mains')).toBeTruthy()
    expect(within(card()).getByTestId('ups-runtime').textContent).toBe('about 15 min')
    expect(within(card()).getByTestId('ups-charge').textContent).toContain('Charge 100 %')
    expect(within(card()).getByText('CP1500PFCLCD')).toBeTruthy()
    expect(within(card()).getByText('42 %')).toBeTruthy()
  })

  test('on battery: orange badge, the amber bar and the UPS flags', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue(status({ state: 'on_battery', charge: 61, flags: ['OB', 'DISCHRG'] }))
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('On battery')).toBeTruthy()
    const bar = within(card()).getByRole('progressbar')
    expect(bar.getAttribute('style')).toContain('width: 61%')
    expect(bar.getAttribute('class')).toContain('bg-amber-500')
    expect(within(card()).getByText('OB DISCHRG')).toBeTruthy()
  })

  test('low battery: red badge and a red bar', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue(status({ state: 'low_battery', charge: 9, runtime_seconds: 60 }))
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('Low battery')).toBeTruthy()
    expect(within(card()).getByRole('progressbar').getAttribute('class')).toContain('bg-red-500')
    expect(within(card()).getByTestId('ups-runtime').textContent).toBe('about 1 min')
  })

  test('not connected: grey badge even when enabled', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue({ enabled: true, connected: false, state: 'unknown', last_error: 'Connection refused' })
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('Not connected')).toBeTruthy()
    expect(within(card()).getByTestId('ups-runtime').textContent).toBe('—')
    expect(within(card()).getByText(/Connection refused/)).toBeTruthy()
    expect(within(card()).getByText('Charge unknown')).toBeTruthy()
  })

  test('disabled: the badge says so', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue({ enabled: false, connected: false, state: 'unknown' })
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('Disabled')).toBeTruthy()
  })

  test('connected but no reading yet: unknown', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue({ enabled: true, connected: true, state: 'unknown' })
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText('Unknown')).toBeTruthy()
  })

  test('the last power event is shown', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue(status({ last_event: { kind: 'ups_on_battery', time: new Date().toISOString() } }))
    wrap(<UPS />)
    await settled()
    expect(within(card()).getByText(/on battery ·/)).toBeTruthy()
  })

  test('the endpoint being unavailable is shown, not a blank card', async () => {
    vi.mocked(api.getUPSStatus).mockRejectedValue(new Error('UPS monitoring is not available'))
    wrap(<UPS />)
    await waitFor(() => expect(within(card()).getByText('Unavailable')).toBeTruthy())
    expect(within(card()).getByRole('alert').textContent).toContain('not available')
  })

  test('saving over a hidden password keeps the stored one', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, mode: 'remote', host: '192.168.1.10', port: 3493, name: 'ups', username: 'monitor', password: '', shutdown_percent: 20 }),
    )
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('Shut down at charge')).toBeTruthy())
    expect((screen.getByLabelText('Shut down at charge') as HTMLInputElement).value).toBe('20')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('ups')
    expect('password' in (value as UPSSettings)).toBe(false)
    expect(value).toMatchObject({ enabled: true, mode: 'remote', host: '192.168.1.10', shutdown_percent: 20 })
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('Saved'))
  })

  test('a password typed once is kept by the next save that only changes a threshold', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: true, mode: 'remote', host: 'h', port: 3493, name: 'ups', username: 'mon' }))
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    await openRemote()
    fireEvent.change(screen.getByLabelText('NUT password'), { target: { value: 'first' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()[1]).toMatchObject({ password: 'first' }))
    expect((screen.getByLabelText('NUT password') as HTMLInputElement).value).toBe('')
    expect(screen.getByLabelText('NUT password').getAttribute('placeholder')).toBe('unchanged')

    fireEvent.change(screen.getByLabelText('Shut down at charge'), { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()[1]).toMatchObject({ password: 'first', shutdown_percent: 30 }))
  })

  test('a visible password survives a save that does not retype it', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, mode: 'remote', host: 'h', port: 3493, name: 'ups', username: 'mon', password: 'adminpw' }),
    )
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    await openRemote()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(lastPatch()[1]).toMatchObject({ password: 'adminpw' }))
  })

  test('remote fields only appear for mode remote', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: true, mode: 'usb' }))
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    expect(screen.queryByLabelText('NUT server')).toBeNull()
    expect(screen.queryByLabelText('NUT password')).toBeNull()
    await openRemote()
    expect(screen.getByLabelText('NUT server')).toBeTruthy()
    expect(screen.getByLabelText('NUT password')).toBeTruthy()
  })

  test('an invalid remote save is shown in the form and never sent', async () => {
    // Enabled and remote with nothing filled in: the password is not required
    // here (no key is stored either, so all four fields must complain).
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: true, mode: 'remote' }))
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getAllByRole('alert').length).toBe(4))
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('a threshold out of range is rejected before saving', async () => {
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('Shut down at runtime')).toBeTruthy())
    fireEvent.change(screen.getByLabelText('Shut down at runtime'), { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('60-3600'))
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('server validation errors land on the right field', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'ups.shutdown_runtime', message: 'want 60-3600 seconds' }],
    })
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('60-3600'))
  })

  test('the stored password is never sent for a USB UPS', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, mode: 'remote', host: 'h', port: 3493, name: 'ups', username: 'mon', password: 'adminpw' }),
    )
    wrap(<UPS />)
    await waitFor(() => expect(screen.getByLabelText('UPS mode')).toBeTruthy())
    fireEvent.change(screen.getByLabelText('UPS mode'), { target: { value: 'usb' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()[1]).toEqual({ enabled: true, mode: 'usb' })
  })
})

const upsWidget: Widget = {
  id: 'w-ups', type: 'ups', title: 'UPS', range: '24h',
  layout: { x: 0, y: 0, w: 4, h: 3 },
}

describe('UPS dashboard widget', () => {
  test('on battery: badge, charge bar and runtime', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue(status({ state: 'on_battery', charge: 55, runtime_seconds: 600, load: 30 }))
    wrap(<WidgetView widget={upsWidget} onChange={() => {}} onRemove={() => {}} />)
    const tile = await waitFor(() => screen.getByTestId('widget-ups'))
    await waitFor(() => expect(within(tile).queryByText('Loading…')).toBeNull())
    expect(within(tile).getByText('On battery')).toBeTruthy()
    expect(within(tile).getByTestId('ups-charge').textContent).toContain('Charge 55 %')
    expect(within(tile).getByTestId('widget-ups-runtime').textContent).toContain('Runtime about 10 min')
    expect(within(tile).getByTestId('widget-ups-runtime').textContent).toContain('load 30 %')
  })

  test('not connected shows a grey badge rather than a blank tile', async () => {
    vi.mocked(api.getUPSStatus).mockResolvedValue({ enabled: true, connected: false, state: 'unknown' })
    wrap(<WidgetView widget={upsWidget} onChange={() => {}} onRemove={() => {}} />)
    const tile = await waitFor(() => screen.getByTestId('widget-ups'))
    await waitFor(() => expect(within(tile).queryByText('Loading…')).toBeNull())
    expect(within(tile).getByText('Not connected')).toBeTruthy()
    expect(within(tile).getByText('Charge unknown')).toBeTruthy()
  })

  test('a widget whose status endpoint is unavailable says so', async () => {
    vi.mocked(api.getUPSStatus).mockRejectedValue(new Error('gone'))
    wrap(<WidgetView widget={upsWidget} onChange={() => {}} onRemove={() => {}} />)
    const tile = await waitFor(() => screen.getByTestId('widget-ups'))
    await waitFor(() => expect(within(tile).getByText(/not available/)).toBeTruthy())
  })
})
