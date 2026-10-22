// Goblin Checks page and dashboard widget (task 109): findings render most
// important first with a working "Fix it" link, Dismiss/Restore call the API
// and refresh the report, the passed list shows what found nothing, and the
// widget shows the score with the high count.
import { beforeEach, describe, expect, it, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/checks', async (orig) => {
  const real = await orig<typeof import('../api/checks')>()
  return { ...real, getChecks: vi.fn(), dismissFinding: vi.fn(), restoreFinding: vi.fn() }
})
vi.mock('../api/auth', async (orig) => {
  const real = await orig<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn() }
})
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
vi.mock('../api/gateways', () => ({ getGatewayStatus: vi.fn() }))
vi.mock('../pages/dashboard/Chart', () => ({ default: () => <div data-testid="chart" /> }))

import ChecksPage from '../pages/Checks'
import Dashboard from '../pages/Dashboard'
import { dismissFinding, getChecks, restoreFinding, type Report } from '../api/checks'
import { getMe } from '../api/auth'
import * as statsApi from '../api/stats'

const high = {
  key: 'wan-open:ssh:eth0',
  check: 'wan-open',
  severity: 'high' as const,
  title: 'SSH is open on the WAN interface eth0',
  detail: 'Anyone on the internet can try to log in.',
  paths: ['interfaces[0].allow_access'],
  link: '/network/interfaces?edit=eth0',
}
const medium = {
  key: 'backup:failing',
  check: 'backup',
  severity: 'medium' as const,
  title: 'Pushing the config history fails',
  detail: 'Last push error: authentication failed.',
  link: '/system?tab=Backup',
}
const low = {
  key: 'upnp:on',
  check: 'upnp',
  severity: 'low' as const,
  title: 'UPnP lets devices open ports by themselves',
  detail: 'A device can open a port without asking you.',
  link: '/network/upnp',
}
const dismissedFinding = {
  key: 'custom-rules:on',
  check: 'custom-rules',
  severity: 'info' as const,
  title: '2 custom nftables rule(s) are active',
  detail: 'Custom rules bypass the built-in checks.',
  link: '/policy/custom-rules',
}

const report = (over: Partial<Report> = {}): Report => ({
  score: 69,
  findings: [high, medium, low],
  dismissed: [dismissedFinding],
  passed: [
    { check: 'guest-lan', title: 'Guests cannot reach the LAN' },
    { check: 'admin-2fa', title: 'Every admin uses a second factor' },
  ],
  ...over,
})

const renderPage = (path = '/checks') =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/checks" element={<ChecksPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )

const renderDashboard = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <Dashboard />
      </MemoryRouter>
    </QueryClientProvider>
  )

beforeEach(() => {
  vi.mocked(getChecks).mockReset().mockResolvedValue(report())
  vi.mocked(dismissFinding).mockReset().mockResolvedValue({ status: 'ok' })
  vi.mocked(restoreFinding).mockReset().mockResolvedValue({ status: 'ok' })
  vi.mocked(getMe).mockReset().mockResolvedValue({ user: 'root', role: 'admin', method: 'session' })
  vi.mocked(statsApi.getDashboards).mockReset().mockResolvedValue({
    version: 1,
    dashboards: [
      { id: 'main', name: 'Main', widgets: [{ id: 'w1', type: 'checks', title: 'Goblin Checks', range: '1h', layout: { x: 0, y: 0, w: 4, h: 3 } }] },
    ],
  })
  vi.mocked(statsApi.saveDashboards).mockReset().mockResolvedValue({ status: 'saved' })
})

describe('Goblin Checks page', () => {
  it('shows the score and the findings grouped high → low', async () => {
    renderPage()
    expect(await screen.findByTestId('checks-score')).toHaveTextContent('69')

    // order on screen: high first, then medium, then low
    const groups = document.querySelectorAll('[data-testid^="group-"]')
    expect(Array.from(groups).map((g) => g.getAttribute('data-testid'))).toEqual([
      'group-high',
      'group-medium',
      'group-low',
    ])
    // an info finding only exists as dismissed, so there is no info group
    expect(screen.queryByTestId('group-info')).not.toBeInTheDocument()

    const order = Array.from(document.querySelectorAll('li[data-testid^="finding-"]')).map((li) =>
      li.getAttribute('data-testid'),
    )
    expect(order).toEqual(['finding-wan-open:ssh:eth0', 'finding-backup:failing', 'finding-upnp:on'])

    expect(screen.getByText('SSH is open on the WAN interface eth0')).toBeInTheDocument()
    expect(screen.getByText('Anyone on the internet can try to log in.')).toBeInTheDocument()
    expect(within(screen.getByTestId('group-high')).getByText('High (1)')).toBeInTheDocument()
  })

  it('"Fix it" links to the finding link', async () => {
    renderPage()
    const row = await screen.findByTestId('finding-wan-open:ssh:eth0')
    const link = within(row).getByText('Fix it')
    expect(link).toHaveAttribute('href', '/network/interfaces?edit=eth0')
    expect(within(screen.getByTestId('finding-backup:failing')).getByText('Fix it')).toHaveAttribute(
      'href',
      '/system?tab=Backup',
    )
  })

  it('dismiss calls the API with the key and refreshes the report', async () => {
    // first answer still shows the finding; once dismissed the server no
    // longer reports it and the score is back up
    vi.mocked(getChecks).mockResolvedValueOnce(report()).mockResolvedValue(report({ score: 89, findings: [medium, low], dismissed: [dismissedFinding, high] }))
    renderPage()
    const row = await screen.findByTestId('finding-wan-open:ssh:eth0')
    fireEvent.click(within(row).getByRole('button', { name: 'Dismiss' }))

    await waitFor(() => expect(dismissFinding).toHaveBeenCalledWith('wan-open:ssh:eth0'))
    await waitFor(() => expect(vi.mocked(getChecks).mock.calls.length).toBeGreaterThan(1))
    expect(await screen.findByTestId('checks-score')).toHaveTextContent('89')
    expect(screen.queryByTestId('finding-wan-open:ssh:eth0')).not.toBeInTheDocument()
  })

  it('restore calls the API with the key and refreshes the report', async () => {
    // the first answer has custom-rules dismissed; after restoring the server
    // puts it back in the findings list and the score drops again
    vi.mocked(getChecks)
      .mockResolvedValueOnce(report())
      .mockResolvedValue(report({ score: 97, findings: [high, medium, low, dismissedFinding], dismissed: [] }))
    renderPage()
    // open the collapsed Dismissed list
    await screen.findByTestId('checks-score')
    fireEvent.click(screen.getByText('1 dismissed'))
    const li = await screen.findByTestId('dismissed-custom-rules:on')
    fireEvent.click(within(li).getByRole('button', { name: 'Restore' }))

    await waitFor(() => expect(restoreFinding).toHaveBeenCalledWith('custom-rules:on'))
    await waitFor(() => expect(vi.mocked(getChecks).mock.calls.length).toBeGreaterThan(1))
    expect(await screen.findByTestId('finding-custom-rules:on')).toBeInTheDocument()
    expect(await screen.findByTestId('checks-score')).toHaveTextContent('97')
  })

  it('shows the server message when dismiss fails', async () => {
    vi.mocked(dismissFinding).mockRejectedValue(new Error('Goblin Checks dismissals are not available on this box'))
    renderPage()
    const row = await screen.findByTestId('finding-wan-open:ssh:eth0')
    fireEvent.click(within(row).getByRole('button', { name: 'Dismiss' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not available on this box')
  })

  it('hides Dismiss and Restore for a read-only user', async () => {
    vi.mocked(getMe).mockResolvedValue({ user: 'guest', role: 'readonly', method: 'session' })
    renderPage()
    const row = await screen.findByTestId('finding-wan-open:ssh:eth0')
    expect(within(row).queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument()
    expect(within(row).getByText('Fix it')).toBeInTheDocument()
    await screen.findByTestId('checks-score')
    fireEvent.click(screen.getByText('1 dismissed'))
    const li = await screen.findByTestId('dismissed-custom-rules:on')
    expect(within(li).queryByRole('button', { name: 'Restore' })).not.toBeInTheDocument()
  })

  it('lists the passed checks in a collapsed section', async () => {
    renderPage()
    const passed = await screen.findByTestId('passed-checks')
    expect(within(passed).getByText('Passed checks: 2')).toBeInTheDocument()
    expect(within(passed).getByText('Guests cannot reach the LAN')).toBeInTheDocument()
    expect(within(passed).getByText('Every admin uses a second factor')).toBeInTheDocument()
  })

  it('says all is well when there is nothing to fix', async () => {
    vi.mocked(getChecks).mockResolvedValue(report({ score: 100, findings: [], dismissed: [] }))
    renderPage()
    expect(await screen.findByTestId('checks-score')).toHaveTextContent('100')
    expect(screen.getByText('Nothing to fix. Everything the checks look at is in good shape.')).toBeInTheDocument()
    expect(screen.getByText('Nothing dismissed.')).toBeInTheDocument()
  })

  it('links the score explanation to the wiki', async () => {
    renderPage()
    const link = await screen.findByRole('link', { name: 'How the score works' })
    expect(link).toHaveAttribute('href', expect.stringContaining('Goblin-Checks'))
  })
})

describe('Goblin Checks dashboard widget', () => {
  test('shows the score and the high count and links to the page', async () => {
    renderDashboard()
    const tile = await screen.findByTestId('widget-checks')
    expect(await within(tile).findByTestId('widget-checks-score')).toHaveTextContent('69')
    expect(within(tile).getByTestId('widget-checks-high')).toHaveTextContent('1 high finding')
    expect(within(tile).getByText('3 findings in total · open Goblin Checks')).toBeInTheDocument()
    expect(getChecks).toHaveBeenCalled()
  })

  test('says so when there are no high findings', async () => {
    vi.mocked(getChecks).mockResolvedValue(report({ score: 97, findings: [low] }))
    renderDashboard()
    const tile = await screen.findByTestId('widget-checks')
    expect(await within(tile).findByTestId('widget-checks-high')).toHaveTextContent('No high findings')
  })
})
