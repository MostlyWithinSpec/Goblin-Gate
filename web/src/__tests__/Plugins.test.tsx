import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Mock the plugins API: no network. The pure helpers stay real.
vi.mock('../api/plugins', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/plugins')>()
  return {
    ...real,
    getPlugins: vi.fn(),
    enablePlugin: vi.fn(),
    disablePlugin: vi.fn(),
    setPluginSettings: vi.fn()
  }
})
import * as api from '../api/plugins'
import Plugins from '../pages/system/Plugins'

const ok = { status: 'ok', validation_errors: null }

const renderPage = (ui: React.ReactElement) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )

// A plugin that asks for three capabilities and has a settings schema covering
// every supported type.
const fullPlugin = (over: Partial<api.PluginStatus> = {}): api.PluginStatus => ({
  name: 'uptime',
  version: '1.2.0',
  description: 'Checks hosts and reports.',
  requested_capabilities: ['events:read', 'stats:read', 'notify:send'],
  capabilities: [],
  enabled: false,
  state: 'stopped',
  settings_schema: {
    type: 'object',
    properties: {
      host: { type: 'string', description: 'Host to check', maxLength: 63, default: 'example.com' },
      mode: { type: 'string', enum: ['ping', 'http'], description: 'How to check' },
      interval: { type: 'integer', minimum: 5, maximum: 3600, description: 'Seconds between checks' },
      ratio: { type: 'number', description: 'Loss ratio threshold' },
      enabled_extra: { type: 'boolean', default: true },
      hosts: { type: 'array', items: { type: 'string' }, description: 'One host per line' }
    }
  },
  ...over
})

beforeEach(() => {
  vi.mocked(api.getPlugins).mockReset().mockResolvedValue({ plugins: [fullPlugin()] })
  vi.mocked(api.enablePlugin).mockReset().mockResolvedValue(ok)
  vi.mocked(api.disablePlugin).mockReset().mockResolvedValue(ok)
  vi.mocked(api.setPluginSettings).mockReset().mockResolvedValue(ok)
})

describe('Plugins page', () => {
  test('lists plugins with state and a trust warning', async () => {
    renderPage(<Plugins />)
    expect(await screen.findByText('uptime')).toBeTruthy()
    expect(screen.getByText('1.2.0')).toBeTruthy()
    expect(screen.getByText('stopped')).toBeTruthy()
    expect(screen.getByRole('note')).toBeTruthy()
  })

  test('enable sends only the checked capabilities', async () => {
    renderPage(<Plugins />)
    fireEvent.click(await screen.findByRole('button', { name: /Enable/ }))

    // The dialog shows all three requested capabilities, checked.
    const caps = await screen.findAllByText(/events:read|stats:read|notify:send/)
    expect(caps.length).toBe(3)
    const boxes = screen.getAllByRole('checkbox') as HTMLInputElement[]
    expect(boxes.every((b) => b.checked)).toBe(true)

    // Uncheck stats:read, then confirm. The dialog renders after the table in
    // DOM order, so its Enable button is the last one.
    const statLabel = screen.getByText('stats:read').closest('label')!
    fireEvent.click(within(statLabel as HTMLElement).getByRole('checkbox'))
    const enableButtons = screen.getAllByRole('button', { name: 'Enable' })
    fireEvent.click(enableButtons[enableButtons.length - 1])

    await waitFor(() => expect(api.enablePlugin).toHaveBeenCalled())
    expect(api.enablePlugin).toHaveBeenCalledWith('uptime', ['events:read', 'notify:send'])
  })

  test('settings form renders each schema type and PUTs the right body', async () => {
    renderPage(<Plugins />)
    fireEvent.click(await screen.findByRole('button', { name: /Settings/ }))

    // String (text), enum (select), integer + number (number inputs), boolean
    // (checkbox), array (textarea). Labels include the description text, so
    // match on the unique description to keep host / hosts apart.
    const host = await screen.findByLabelText(/Host to check/)
    expect(host.tagName).toBe('INPUT')
    const mode = screen.getByLabelText(/mode/)
    expect(mode.tagName).toBe('SELECT')
    expect(within(mode as HTMLElement).getAllByRole('option').length).toBe(3) // placeholder + 2
    const interval = screen.getByLabelText(/interval/)
    expect(interval.tagName).toBe('INPUT')
    expect((interval as HTMLInputElement).type).toBe('number')
    const ratio = screen.getByLabelText(/ratio/)
    expect((ratio as HTMLInputElement).type).toBe('number')
    const bool = screen.getByLabelText(/enabled_extra/) as HTMLInputElement
    expect(bool.type).toBe('checkbox')
    expect(bool.checked).toBe(true) // seeded from default: true
    const hosts = screen.getByLabelText(/hosts/)
    expect(hosts.tagName).toBe('TEXTAREA')

    // Fill the form.
    fireEvent.change(host, { target: { value: 'host1.example.com' } })
    fireEvent.change(mode, { target: { value: 'http' } })
    fireEvent.change(interval, { target: { value: '30' } })
    fireEvent.change(ratio, { target: { value: '0.5' } })
    fireEvent.change(hosts, { target: { value: 'a.com\nb.com\n' } })

    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.setPluginSettings).toHaveBeenCalled())
    expect(api.setPluginSettings).toHaveBeenCalledWith('uptime', {
      host: 'host1.example.com',
      mode: 'http',
      interval: 30,
      ratio: 0.5,
      enabled_extra: true,
      hosts: ['a.com', 'b.com']
    })
  })

  test('a 400 validation_failed shows field errors', async () => {
    vi.mocked(api.setPluginSettings).mockRejectedValueOnce(
      new Error('validation failed') as Error & { details?: unknown } &&
        (Object.assign(new Error('validation failed'), { details: ['interval: must be at least 5'] }) as Error & { details?: unknown })
    )
    renderPage(<Plugins />)
    fireEvent.click(await screen.findByRole('button', { name: /Settings/ }))

    const interval = await screen.findByLabelText(/interval/)
    fireEvent.change(interval, { target: { value: '1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.getByText('interval: must be at least 5')).toBeTruthy()
  })

  test('an invalid plugin shows its problem and cannot be enabled', async () => {
    vi.mocked(api.getPlugins).mockResolvedValue({
      plugins: [fullPlugin({ state: 'invalid', problem: 'manifest missing program' })]
    })
    renderPage(<Plugins />)
    expect(await screen.findByText('manifest missing program')).toBeTruthy()
    // No Enable button for an invalid plugin.
    expect(screen.queryByRole('button', { name: /Enable/ })).toBeNull()
    expect(api.enablePlugin).not.toHaveBeenCalled()
  })

  test('disable a running plugin posts to the API', async () => {
    vi.mocked(api.getPlugins).mockResolvedValue({ plugins: [fullPlugin({ enabled: true, state: 'running' })] })
    renderPage(<Plugins />)
    fireEvent.click(await screen.findByRole('button', { name: /Disable/ }))
    await waitFor(() => expect(api.disablePlugin).toHaveBeenCalledWith('uptime'))
  })
})

describe('plugin page view', () => {
  test('shows the page in a sandboxed iframe without same-origin access', async () => {
    const { MemoryRouter, Routes, Route } = await import('react-router-dom')
    const { default: PluginPage } = await import('../pages/system/PluginPage')
    render(
      <MemoryRouter initialEntries={['/system/plugins/hello/status.html']}>
        <Routes>
          <Route path="/system/plugins/:name/*" element={<PluginPage />} />
        </Routes>
      </MemoryRouter>,
    )
    const frame = screen.getByTitle('Plugin hello page status.html')
    expect(frame).toHaveAttribute('src', '/plugin-ui/hello/status.html')
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts allow-forms')
  })
})
