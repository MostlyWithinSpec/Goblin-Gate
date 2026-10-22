import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

// Mock the API modules: no network. The pure helpers stay real.
vi.mock('../api/objects', async (orig) => {
  const real = await orig<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn(), getIPSRules: vi.fn() }
})
vi.mock('../api/stats', async (orig) => {
  const real = await orig<typeof import('../api/stats')>()
  return { ...real, listLogs: vi.fn() }
})
import * as api from '../api/objects'
import * as stats from '../api/stats'
import IPS, { RuleSource } from '../pages/security/IPS'
import { addSuppress, groupBySignature, parseSignature, validateIPS } from '../pages/security/profileLogic'

const cfg: Candidate = {
  profiles: { ips: [{ name: 'home' }, { name: 'strict', mode: 'prevent', block_severity: 3, categories: ['MALWARE'], suppress: [{ sid: 1, track: 'by_src', ip: '192.168.1.9' }] }] },
}
const events: stats.LogEvent[] = [
  { id: 3, time: 1760000300, kind: 'ips', action: 'drop', src_ip: '203.0.113.9', src_port: 4444, dst_ip: '192.168.1.20', dst_port: 80, severity: '1', signature: '2010935: ET SCAN Suspicious inbound to MSSQL' },
  { id: 2, time: 1760000200, kind: 'ips', action: 'alert', src_ip: '192.168.1.20', dst_ip: '203.0.113.80', severity: '2', signature: '2100498: GPL ATTACK_RESPONSE id check returned root' },
  { id: 1, time: 1760000100, kind: 'ips', action: 'alert', src_ip: '192.168.1.21', dst_ip: '203.0.113.80', severity: '2', signature: '2100498: GPL ATTACK_RESPONSE id check returned root' },
]
const lastPatch = () => {
  const c = vi.mocked(api.patchSection).mock.calls
  return c[c.length - 1]
}
const show = () => render(<MemoryRouter><IPS /></MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockImplementation(async () => structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(stats.listLogs).mockResolvedValue({ events, next_cursor: 0 })
  vi.mocked(api.getIPSRules).mockResolvedValue({ ruleset: 'ET Open', rules: 48000, last_update: '2026-10-22T03:00:00Z' })
})

describe('IPS logic', () => {
  test('signatures, exceptions, validation, grouping', () => {
    expect(parseSignature('2100498: GPL x')).toEqual({ sid: 2100498, msg: 'GPL x' })
    expect(parseSignature('no id')).toEqual({ sid: null, msg: 'no id' })
    const p = addSuppress<{ name: string; suppress?: { sid: number; track?: string; ip?: string }[] }>({ name: 'a' }, { sid: 5, track: 'by_src', ip: '1.2.3.4' })
    expect(addSuppress(p, { sid: 5, track: 'by_src', ip: '1.2.3.4' })).toBe(p)
    expect(validateIPS({ block_severity: 4 }).block_severity).toBeDefined()
    expect(validateIPS({ categories: ['malware'] }).categories).toMatch(/Not a category/)
    expect(validateIPS({ suppress: [{ sid: 5, track: 'by_dst' }] }).suppress).toMatch(/IPv4/)
    expect(validateIPS({ suppress: [{ sid: 5 }], categories: ['MALWARE'], block_severity: 1 })).toEqual({})
    expect(groupBySignature(events).map((g) => [g.count, g.last])).toEqual([[2, 1760000200], [1, 1760000300]])
  })
})

describe('IPS page', () => {
  test('profiles table', async () => {
    show()
    const row = (await screen.findByRole('button', { name: 'strict' })).closest('tr')!
    expect(within(row).getByText('all')).toBeInTheDocument()
    expect(within(row).getByText('MALWARE')).toBeInTheDocument()
    expect(within(screen.getByRole('button', { name: 'home' }).closest('tr')!).getByText('high + medium')).toBeInTheDocument()
  })

  test('new profile: defaults are not stored, categories validated', async () => {
    show()
    await screen.findByRole('button', { name: 'home' })
    fireEvent.click(screen.getByRole('button', { name: /New IPS profile/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'lab' } })
    fireEvent.change(screen.getByLabelText('Mode'), { target: { value: 'detect' } })
    fireEvent.click(screen.getByLabelText('SCAN'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['profiles.ips', [...cfg.profiles!.ips!, { name: 'lab', mode: 'detect', categories: ['SCAN'] }]])
  })

  test('events table with severity chips and src → dst', async () => {
    show()
    const t = await screen.findByTestId('ips-events')
    await within(t).findByText('ET SCAN Suspicious inbound to MSSQL')
    expect(within(t).getByText('203.0.113.9:4444 → 192.168.1.20:80')).toBeInTheDocument()
    expect(within(t).getByText('high')).toBeInTheDocument()
    expect(stats.listLogs).toHaveBeenCalledWith({ kind: 'ips', from: '-24h', limit: 500 })
  })

  test('group by signature links to the log viewer', async () => {
    show()
    const t = await screen.findByTestId('ips-events')
    await within(t).findByText('ET SCAN Suspicious inbound to MSSQL')
    fireEvent.click(screen.getByLabelText('Group by signature'))
    const link = within(t).getByRole('link', { name: 'GPL ATTACK_RESPONSE id check returned root' })
    expect(link.getAttribute('href')).toContain('signature=2100498')
    expect(link.closest('tr')!).toHaveTextContent('2')
  })

  test('create exception adds a suppress entry to the chosen profile', async () => {
    show()
    const t = await screen.findByTestId('ips-events')
    const row = (await within(t).findByText('ET SCAN Suspicious inbound to MSSQL')).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'Create exception' }))
    await screen.findByText('2010935')
    await waitFor(() => expect(screen.getByLabelText('Profile')).toHaveValue('home'))
    fireEvent.change(screen.getByLabelText('Profile'), { target: { value: 'strict' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add exception' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['profiles.ips', [{ name: 'home' }, { ...cfg.profiles!.ips![1], suppress: [{ sid: 1, track: 'by_src', ip: '192.168.1.9' }, { sid: 2010935, track: 'by_src', ip: '203.0.113.9' }] }]])
    expect(await screen.findByRole('status')).toHaveTextContent('Exception for rule 2010935 added to strict')
  })
})

describe('IPS rule source (ET Pro)', () => {
  test('shows the rule set and saves a typed code', async () => {
    render(<RuleSource />)
    expect(await screen.findByText('ET Open')).toBeTruthy()
    expect(screen.getByTestId('ips-ruleset').textContent).toContain('48,000 rules')
    expect(screen.queryByRole('button', { name: 'Remove code' })).toBeNull()
    fireEvent.change(screen.getByLabelText('ET Pro code'), { target: { value: ' 0123456789abcdef ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save code' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledWith('ips', { etpro_code: '0123456789abcdef' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Commit to switch to ET Pro')
    expect((screen.getByLabelText('ET Pro code') as HTMLInputElement).value).toBe('')
  })

  test('a stored code shows "unchanged" and can be removed', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({ ips: { etpro_code: 'abcdef0123456789' } })
    render(<RuleSource />)
    await waitFor(() => expect((screen.getByLabelText('ET Pro code') as HTMLInputElement).placeholder).toBe('unchanged'))
    // the code itself is never put in the page
    expect(document.body.innerHTML).not.toContain('abcdef0123456789')
    fireEvent.click(screen.getByRole('button', { name: 'Remove code' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledWith('ips', {}))
  })

  test('a refused code and a failed download are shown', async () => {
    vi.mocked(api.getIPSRules).mockResolvedValue({ ruleset: 'ET Pro', last_error: 'suricata-update: 403 Forbidden' })
    vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: [{ path: 'ips.etpro_code', message: 'want the ET Pro code' }] })
    render(<RuleSource />)
    expect(await screen.findByText(/Last download failed: suricata-update: 403/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText('ET Pro code'), { target: { value: 'bad code' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save code' }))
    expect(await screen.findByText('want the ET Pro code')).toBeTruthy()
  })
})
