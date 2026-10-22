import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/system', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/system')>()
  return { ...real, importPfSense: vi.fn() }
})
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getDevices: vi.fn() }
})

import * as systemApi from '../api/system'
import * as objectsApi from '../api/objects'
import System from '../pages/System'

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

const mockResult: systemApi.ImportResult = {
  report: [
    { section: 'policies', item: 'lan-out', status: 'converted' },
    { section: 'policies', item: 'guest-wan', status: 'converted' },
    { section: 'nat', item: 'port-forward-all', status: 'approximated', detail: 'All ports forwarded — review scope', risk: true },
    { section: 'dhcp', item: 'relay', status: 'skipped', detail: 'DHCP relay not supported' },
  ],
  interfaces: [
    { pfsense: 'wan', pfsense_device: 'em0', description: 'WAN', name: 'wan1', role: 'wan', device: '' },
    { pfsense: 'lan', pfsense_device: 'em1', description: 'LAN', name: 'lan', role: 'lan', device: 'eth1' },
  ],
  summary: { converted: 2, approximated: 1, skipped: 1 },
}

const mockResultNoUnassigned: systemApi.ImportResult = {
  report: [
    { section: 'policies', item: 'lan-out', status: 'converted' },
    { section: 'nat', item: 'port-forward-all', status: 'approximated', detail: 'All ports forwarded — review scope', risk: true },
  ],
  interfaces: [
    { pfsense: 'wan', pfsense_device: 'em0', name: 'wan1', role: 'wan', device: 'eth0' },
    { pfsense: 'lan', pfsense_device: 'em1', name: 'lan', role: 'lan', device: 'eth1' },
  ],
  summary: { converted: 1, approximated: 1, skipped: 0 },
}

beforeEach(() => {
  vi.mocked(systemApi.importPfSense).mockReset().mockResolvedValue(structuredClone(mockResult))
  vi.mocked(objectsApi.getDevices).mockReset().mockResolvedValue([
    { name: 'eth0', up: true },
    { name: 'eth1', up: true },
  ])
})

describe('Import tab', () => {
  test('renders upload step with file input and warning', async () => {
    wrap(<System />)
    // Click Import tab
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    expect(await screen.findByText(/Upload a pfSense or OPNsense config\.xml/)).toBeInTheDocument()
    expect(screen.getByText(/replaces the current candidate/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Upload firewall config')).toBeInTheDocument()
  })

  test('upload triggers importPfSense and shows assign step when unassigned interfaces exist', async () => {
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    // Should call importPfSense with the file (no devices on first pass)
    await waitFor(() => expect(systemApi.importPfSense).toHaveBeenCalledTimes(1))
    const call = vi.mocked(systemApi.importPfSense).mock.calls[0]
    expect(call[0]).toBe(file)
    expect(call[1]).toBeUndefined()

    // Should show the assign step since 'wan' has device: ''
    await waitFor(() => expect(screen.getByText('Assign devices to interfaces')).toBeInTheDocument())
    // The unassigned interface (wan) should be in the table
    expect(screen.getAllByText('wan').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByLabelText('Device for wan')).toBeInTheDocument()
  })

  test('an OPNsense backup is named as such when assigning devices', async () => {
    vi.mocked(systemApi.importPfSense).mockResolvedValue({ ...structuredClone(mockResult), source: 'OPNsense' })
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    fireEvent.change(input, { target: { files: [new File(['<xml/>'], 'config.xml', { type: 'application/xml' })] } })
    await waitFor(() => expect(screen.getByText('Assign devices to interfaces')).toBeInTheDocument())
    expect(screen.getByRole('columnheader', { name: 'OPNsense' })).toBeInTheDocument()
    expect(screen.getByText(/backs each OPNsense interface/)).toBeInTheDocument()
  })

  test('skips assign step when all interfaces are already assigned', async () => {
    vi.mocked(systemApi.importPfSense).mockResolvedValue(structuredClone(mockResultNoUnassigned))
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    // Should go straight to report (no assign step)
    await waitFor(() => expect(screen.getByText('Review changes')).toBeInTheDocument())
  })

  test('report groups entries by status', async () => {
    vi.mocked(systemApi.importPfSense).mockResolvedValue(structuredClone(mockResultNoUnassigned))
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    // Wait for report to render
    await waitFor(() => expect(screen.getByText('Review changes')).toBeInTheDocument())

    // Summary counts
    expect(screen.getByText('1 converted')).toBeInTheDocument()
    expect(screen.getByText('1 approximated')).toBeInTheDocument()
    expect(screen.getByText('0 skipped')).toBeInTheDocument()

    // Grouped sections
    expect(screen.getByText(/Converted \(1\)/)).toBeInTheDocument()
    expect(screen.getByText(/Approximated \(1\)/)).toBeInTheDocument()
    expect(screen.getByText(/Skipped \(0\)/)).toBeInTheDocument()

    // Individual items appear
    expect(screen.getByText('lan-out')).toBeInTheDocument()
    expect(screen.getByText('port-forward-all')).toBeInTheDocument()
  })

  test('risk entries are highlighted in a Warnings section', async () => {
    vi.mocked(systemApi.importPfSense).mockResolvedValue(structuredClone(mockResultNoUnassigned))
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(screen.getByText('Review changes')).toBeInTheDocument())

    // Warnings section header present
    const warnings = screen.getByText('Warnings')
    expect(warnings).toBeInTheDocument()
    // The risk item's detail is shown in the warning (appears in both Warnings and Approximated sections)
    expect(screen.getAllByText(/All ports forwarded — review scope/).length).toBeGreaterThanOrEqual(1)
  })

  test('apply assignment calls importPfSense with device map', async () => {
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    // Wait for assign step
    await waitFor(() => expect(screen.getByText('Assign devices to interfaces')).toBeInTheDocument())

    // Select a device for wan
    const select = screen.getByLabelText('Device for wan') as HTMLSelectElement
    fireEvent.change(select, { target: { value: 'eth0' } })

    // Click Apply
    fireEvent.click(screen.getByRole('button', { name: /Apply assignment/ }))

    await waitFor(() => expect(systemApi.importPfSense).toHaveBeenCalledTimes(2))
    const secondCall = vi.mocked(systemApi.importPfSense).mock.calls[1]
    expect(secondCall[0]).toBe(file)
    expect(secondCall[1]).toEqual({ wan: 'eth0' })

    // Should now show report
    await waitFor(() => expect(screen.getByText('Review changes')).toBeInTheDocument())
  })

  test('shows error when import fails', async () => {
    vi.mocked(systemApi.importPfSense).mockRejectedValue(new Error('HTTP 500'))
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('HTTP 500'))
  })

  test('shows validation errors when present in result', async () => {
    const resultWithErrors: systemApi.ImportResult = {
      ...mockResultNoUnassigned,
      validation_errors: [{ path: 'policies[0]', message: 'missing dst_intf' }],
    }
    vi.mocked(systemApi.importPfSense).mockResolvedValue(resultWithErrors)
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(screen.getByText(/Validation errors/)).toBeInTheDocument())
    // The message is split across text nodes by the <span>, so check via container text
    const list = screen.getByText(/Validation errors/).closest('div')!
    expect(list.textContent).toContain('missing dst_intf')
  })

  test('Review changes link points to revisions', async () => {
    vi.mocked(systemApi.importPfSense).mockResolvedValue(structuredClone(mockResultNoUnassigned))
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    const input = await screen.findByLabelText('Upload firewall config')
    const file = new File(['<xml/>'], 'config.xml', { type: 'application/xml' })
    fireEvent.change(input, { target: { files: [file] } })

    const link = await screen.findByText('Review changes')
    expect(link.closest('a')).toHaveAttribute('href', '/system/revisions')
  })
})
