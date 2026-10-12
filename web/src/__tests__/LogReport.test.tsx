import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'

// Mock the API module: no network. The pure helpers stay real.
vi.mock('../api/stats', async (orig) => {
  const real = await orig<typeof import('../api/stats')>()
  return { ...real, listLogs: vi.fn(), statsQuery: vi.fn(), getDashboards: vi.fn(), saveDashboards: vi.fn() }
})
vi.mock('../pages/dashboard/Chart', () => ({
  default: ({ option }: { option: { series?: { type: string }[] } }) => <div data-testid="chart">{option.series?.[0]?.type}</div>,
}))
import * as api from '../api/stats'
import type { LogEvent } from '../api/stats'
import Log from '../pages/Log'
import { builderQuery, emptyBuilder, logFilter, parseLogParams, validateBuilder } from '../pages/log/logLogic'

const last = <T,>(xs: T[]): T | undefined => xs[xs.length - 1]
const ev = (id: number, e: Partial<LogEvent>): LogEvent => ({ id, time: 1760000000 + id, kind: 'traffic', action: 'deny', ...e })

let where = ''
function Where() {
  where = useLocation().search
  return null
}
const open = (url: string) =>
  render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/log" element={<><Log /><Where /></>} />
        <Route path="/policy" element={<p>policy page</p>} />
      </Routes>
    </MemoryRouter>,
  )

beforeEach(() => {
  vi.mocked(api.listLogs).mockReset().mockResolvedValue({
    events: [
      ev(3, { src_ip: '192.168.1.20', dst_ip: '8.8.8.8', dst_port: 53, proto: 'udp', policy_id: '7', in_intf: 'lan', out_intf: 'wan1' }),
      ev(2, { kind: 'ips', action: 'alert', src_ip: '203.0.113.9', signature: 'ET SCAN Nmap', severity: '2' }),
    ],
    next_cursor: 2,
  })
  vi.mocked(api.statsQuery).mockReset().mockResolvedValue({ columns: ['src_ip', 'count'], rows: [['192.168.1.20', 12]] })
  vi.mocked(api.getDashboards).mockResolvedValue({ version: 1, dashboards: [{ id: 'main', name: 'Main', widgets: [] }] })
  vi.mocked(api.saveDashboards).mockReset().mockResolvedValue({ status: 'saved' })
})

describe('log logic', () => {
  test('reads drill-down links and builds the filter', () => {
    const s = parseLogParams('?kind=traffic&src_ip=192.168.1.5&from=-24h&bogus=1&q=x')
    expect(s).toEqual({ kind: 'traffic', q: 'x', range: '24h', chips: { src_ip: '192.168.1.5' } })
    expect(logFilter(s, 9)).toEqual({ kind: 'traffic', q: 'x', from: '-24h', fields: { src_ip: '192.168.1.5' }, cursor: 9, limit: 100 })
    expect(parseLogParams('?kind=nope').kind).toBe('')
  })

  test('builder validation', () => {
    expect(validateBuilder(emptyBuilder())).toEqual({})
    expect(validateBuilder({ ...emptyBuilder(), chart: 'line' }).bucket).toMatch(/time bucket/)
    expect(validateBuilder({ ...emptyBuilder(), chart: 'pie', bucket: '5m' }).bucket).toMatch(/pie/)
    expect(validateBuilder({ ...emptyBuilder(), source: 'rollup', group_by: ['src_ip'] }).group_by).toMatch(/rollup cannot be grouped by src_ip/)
    expect(validateBuilder({ ...emptyBuilder(), group_by: ['a', 'b', 'c'].map(() => 'kind') }).group_by).toBeDefined()
    expect(builderQuery({ ...emptyBuilder(), bucket: 'auto', range: '24h' })).toMatchObject({ bucket: '1h', limit: 1000 })
  })
})

describe('LogViewer', () => {
  test('lists events and asks the API with the URL filters', async () => {
    open('/log?kind=traffic&src_ip=192.168.1.20')
    expect(await screen.findByText('8.8.8.8')).toBeInTheDocument()
    expect(api.listLogs).toHaveBeenCalledWith({ kind: 'traffic', q: undefined, from: '-1h', fields: { src_ip: '192.168.1.20' }, cursor: 0, limit: 100 })
    expect(screen.getByRole('tab', { name: 'Traffic' })).toHaveAttribute('aria-selected', 'true')
    expect(within(screen.getByTestId('chips')).getByText('src_ip = 192.168.1.20')).toBeInTheDocument()
  })

  test('chips: add and remove update the URL and the query', async () => {
    open('/log')
    await screen.findByText('8.8.8.8')
    fireEvent.change(screen.getByLabelText('Filter field'), { target: { value: 'dst_port' } })
    fireEvent.change(screen.getByLabelText('Filter value'), { target: { value: '53' } })
    fireEvent.click(screen.getByRole('button', { name: /Add filter/ }))
    await waitFor(() => expect(where).toBe('?dst_port=53'))
    await waitFor(() => expect(last(vi.mocked(api.listLogs).mock.calls)?.[0]?.fields).toEqual({ dst_port: '53' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove filter dst_port' }))
    await waitFor(() => expect(where).toBe(''))
  })

  test('kind tab changes the columns', async () => {
    open('/log')
    await screen.findByText('8.8.8.8')
    fireEvent.click(screen.getByRole('tab', { name: 'IPS' }))
    await waitFor(() => expect(where).toBe('?kind=ips'))
    expect(screen.getByRole('columnheader', { name: 'Signature' })).toBeInTheDocument()
  })

  test('load more pages with the cursor', async () => {
    open('/log')
    await screen.findByText('8.8.8.8')
    vi.mocked(api.listLogs).mockResolvedValueOnce({ events: [ev(1, { dst_ip: '1.1.1.1' })], next_cursor: 0 })
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('1.1.1.1')).toBeInTheDocument()
    expect(last(vi.mocked(api.listLogs).mock.calls)?.[0]?.cursor).toBe(2)
    expect(screen.getByText('8.8.8.8')).toBeInTheDocument()
    expect(screen.getByText('End of results')).toBeInTheDocument()
  })

  test('detail drawer shows all fields, filters on a value and links to the policy', async () => {
    open('/log')
    fireEvent.click(await screen.findByText('8.8.8.8'))
    expect(screen.getByText('lan')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to policy 7' })).toHaveAttribute('href', '/policy?edit=7')
    fireEvent.click(screen.getByRole('button', { name: 'Filter on out_intf' }))
    await waitFor(() => expect(where).toBe('?out_intf=wan1'))
  })
})

describe('ChartBuilder', () => {
  test('previews the query and saves it as a widget', async () => {
    open('/log?view=charts')
    expect(await screen.findByTestId('chart')).toHaveTextContent('bar')
    expect(api.statsQuery).toHaveBeenCalledWith({ source: 'events', metric: 'count', group_by: ['src_ip'], filters: undefined, bucket: 'none', limit: 20, from: '-1h' })
    fireEvent.change(screen.getByLabelText('Widget title'), { target: { value: 'Top clients' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save as widget' }))
    await waitFor(() => expect(api.saveDashboards).toHaveBeenCalled())
    const saved = vi.mocked(api.saveDashboards).mock.calls[0][0]
    expect(saved.dashboards[0].widgets[0]).toMatchObject({ type: 'custom', title: 'Top clients', chart: 'bar', range: '1h', query: { group_by: ['src_ip'], bucket: 'none' } })
    expect(await screen.findByRole('status')).toHaveTextContent('Added "Top clients" to Main.')
  })

  test('invalid combinations block the preview and saving', async () => {
    open('/log?view=charts')
    await screen.findByTestId('chart')
    fireEvent.change(screen.getByLabelText('Chart type'), { target: { value: 'line' } })
    expect(await screen.findByText(/need a time bucket/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save as widget' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Bucket'), { target: { value: 'auto' } })
    await waitFor(() => expect(last(vi.mocked(api.statsQuery).mock.calls)?.[0]).toMatchObject({ bucket: '5m' }))
  })

  test('switching to rollups drops fields rollups lack', async () => {
    open('/log?view=charts')
    await screen.findByTestId('chart')
    fireEvent.change(screen.getByLabelText('Source'), { target: { value: 'rollup' } })
    await waitFor(() => expect(last(vi.mocked(api.statsQuery).mock.calls)?.[0]).toMatchObject({ source: 'rollup', group_by: undefined }))
  })
})
