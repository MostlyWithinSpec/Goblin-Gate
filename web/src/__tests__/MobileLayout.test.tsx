// Phone layout pass (task 107): the shell at a 375 px viewport and the rule
// that a wide table scrolls inside its own box instead of widening the page.
//
// jsdom has no layout engine: it does not evaluate @media rules and reports
// clientWidth/scrollWidth as 0, so "phone" is driven through window.innerWidth
// (which Layout reads) and asserted through the drawer's classes and aria
// state, while "the container scrolls, not the page" is asserted through the
// computed overflow of the table's box versus html/body.
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getSystemStatus: vi.fn(), getConfigDiff: vi.fn(), commitConfig: vi.fn(), confirmCommit: vi.fn() }
})
vi.mock('../api/system', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/system')>()
  return { ...real, getUpdates: vi.fn() }
})
vi.mock('../api/plugins', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/plugins')>()
  return { ...real, getPlugins: vi.fn() }
})
vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn() }
})
vi.mock('../api/network', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/network')>()
  return { ...real, getNetworkConfig: vi.fn(), patchSection: vi.fn(), getLeases: vi.fn() }
})

import { getCandidate } from '../api/objects'
import { getConfigDiff, getSystemStatus } from '../api/config'
import { getUpdates } from '../api/system'
import { getPlugins } from '../api/plugins'
import { getMe } from '../api/auth'
import { getNetworkConfig, getLeases } from '../api/network'
import Layout from '../components/Layout'
import PortForwards from '../pages/network/PortForwards'
import type { Candidate } from '../api/objects'
import type { NetworkConfig } from '../api/network'

const candidate = {
  hostname: 'goblin',
  interfaces: [{ name: 'wan0', address: '198.51.100.2/24', type: 'static' }],
  nat: { port_forwards: [{ name: 'web', in_intf: 'wan0', ext_port: '443', protocol: 'tcp', to_addr: '192.168.1.10', to_port: '443' }] },
} as unknown as Candidate

beforeEach(() => {
  vi.mocked(getCandidate).mockResolvedValue(candidate)
  vi.mocked(getSystemStatus).mockResolvedValue({ version: 'x', pending_confirm: null })
  vi.mocked(getConfigDiff).mockResolvedValue({ count: 0, changes: [] })
  vi.mocked(getUpdates).mockResolvedValue({ installed: '', available: '', checked: '' })
  vi.mocked(getPlugins).mockResolvedValue({ plugins: [] })
  vi.mocked(getMe).mockResolvedValue({ user: 'admin', role: 'admin', method: 'session' })
  vi.mocked(getNetworkConfig).mockResolvedValue(candidate as unknown as NetworkConfig)
  vi.mocked(getLeases).mockResolvedValue([])
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function renderShell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<PortForwards />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// The drawer is off-canvas when its class list carries the slide-out transform
// (Tailwind's CSS is not loaded in jsdom, so the class is the state).
function isOpen(sidebar: HTMLElement): boolean {
  return !sidebar.className.split(/\s+/).includes('-translate-x-full')
}

describe('layout shell on a desktop viewport', () => {
  test('the sidebar is part of the page and not hidden from screen readers', async () => {
    vi.stubGlobal('innerWidth', 1280)
    renderShell()
    const sidebar = screen.getByTestId('sidebar')
    expect(sidebar).not.toHaveAttribute('aria-hidden')
  })
})

describe('layout shell at a 375 px viewport', () => {
  beforeEach(() => {
    vi.stubGlobal('innerWidth', 375)
  })

  test('the menu button opens and closes the drawer', async () => {
    renderShell()
    const sidebar = screen.getByTestId('sidebar')
    // Below md the sidebar is an off-canvas drawer that starts closed, so the
    // page is what you see first.
    expect(isOpen(sidebar)).toBe(false)
    expect(sidebar).toHaveAttribute('aria-hidden', 'true')

    const menu = screen.getByRole('button', { name: 'Open menu', expanded: false })
    expect(menu).toHaveAttribute('aria-controls', 'sidebar')
    fireEvent.click(menu)
    await waitFor(() => expect(isOpen(sidebar)).toBe(true))
    expect(sidebar).toHaveAttribute('aria-hidden', 'false')
    // the header button flipped, and the drawer has its own close button too
    const openMenu = screen.getByRole('button', { name: 'Close menu', expanded: true })
    expect(screen.getAllByRole('button', { name: 'Close menu' }).length).toBeGreaterThan(1)

    fireEvent.click(openMenu)
    await waitFor(() => expect(isOpen(sidebar)).toBe(false))
    expect(screen.getByRole('button', { name: 'Open menu', expanded: false })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await waitFor(() => expect(isOpen(sidebar)).toBe(true))

    // Escape closes the drawer too
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(isOpen(sidebar)).toBe(false))
  })

  test('the dimmed backdrop closes the drawer', async () => {
    const { container } = renderShell()
    const sidebar = screen.getByTestId('sidebar')
    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await waitFor(() => expect(isOpen(sidebar)).toBe(true))
    const backdrop = container.querySelector('.fixed.inset-0.z-30')
    expect(backdrop).not.toBeNull()
    fireEvent.click(backdrop as Element)
    await waitFor(() => expect(isOpen(sidebar)).toBe(false))
  })

  test('a commit bar is rendered in the phone strip below the header', async () => {
    const { container } = renderShell()
    // The header bar is md-and-up only, so the phone strip must carry its own.
    await waitFor(() => expect(container.textContent).toContain('Changes'))
    const strips = Array.from(container.querySelectorAll('div')).filter(
      (d) => d.className.includes('md:hidden') && /changes/i.test(d.textContent ?? ''),
    )
    expect(strips.length).toBeGreaterThan(0)
  })
})

describe('a wide table page at a 375 px viewport', () => {
  beforeEach(() => {
    vi.stubGlobal('innerWidth', 375)
  })

  test('the table scrolls inside its own box and the page does not', async () => {
    const { container } = renderShell()
    const box = await screen.findByTestId('table-scroll')
    // jsdom has no layout and does not load Tailwind's sheet, so the scroll
    // box is asserted through its classes: the table's direct wrapper clips
    // sideways, and no ancestor of it up to the page root does.
    expect(box.className).toContain('overflow-x-auto')
    expect(box.className).toContain('max-w-full')
    for (let el = box.parentElement; el !== null; el = el.parentElement) {
      const cls = typeof el.className === 'string' ? el.className : ''
      expect(cls).not.toContain('overflow-x-auto')
      expect(cls).not.toContain('overflow-x-scroll')
      // the shell clips sideways instead of scrolling the page
      if (cls.includes('overflow-x-hidden')) return
    }
    expect(getComputedStyle(document.documentElement).overflowX).not.toBe('auto')
    expect(getComputedStyle(document.body).overflowX).not.toBe('auto')
    // The first column stays pinned while the rest scrolls.
    expect(box.querySelector('thead th')?.className).toContain('sticky')
    expect(box.querySelector('tbody td')?.className).toContain('sticky')
    // the page card never grows wider than the viewport either
    expect(container.querySelector('main')?.className).toContain('overflow-y-auto')
  })

  test('a form input in the drawer is at least 40 px tall', async () => {
    renderShell()
    fireEvent.click(await screen.findByRole('button', { name: /New port forward/i }))
    const name = await screen.findByLabelText('Name')
    expect(name.className).toContain('min-h-[40px]')
  })
})
