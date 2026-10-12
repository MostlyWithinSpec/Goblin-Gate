import { afterEach, describe, expect, test, vi } from 'vitest'
import { bucketFor, fieldsFor, getDashboards, listLogs, rangeToFrom, statsQuery } from '../api/stats'

const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))

afterEach(() => {
  vi.restoreAllMocks()
})

describe('stats api', () => {
  test('statsQuery posts the query', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ columns: ['dst_ip', 'count'], rows: [['10.0.0.1', 2]] }))
    const res = await statsQuery({ group_by: ['dst_ip'], from: '-1h' })
    expect(res.rows[0]).toEqual(['10.0.0.1', 2])
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/v1/stats/query')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(init?.body as string)).toEqual({ group_by: ['dst_ip'], from: '-1h' })
  })

  test('listLogs builds the query string and skips empty values', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ events: [], next_cursor: 0 }))
    await listLogs({ kind: 'ips', q: '', cursor: 0, limit: 50 })
    expect(f.mock.calls[0][0]).toBe('/api/v1/logs?kind=ips&limit=50')
  })

  test('getDashboards fills in an empty document', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => json({ dashboards: [] }))
    expect(await getDashboards()).toEqual({ version: 1, dashboards: [] })
  })

  test('helpers', () => {
    expect(rangeToFrom('24h')).toBe('-24h')
    expect(bucketFor('15m')).toBe('1m')
    expect(bucketFor('7d')).toBe('1h')
    expect(fieldsFor('rollup')).not.toContain('src_ip')
  })
})
