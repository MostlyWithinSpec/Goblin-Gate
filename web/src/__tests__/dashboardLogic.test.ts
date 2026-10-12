import { describe, expect, test } from 'vitest'
import { addBuiltin, addCustom, applyLayout, BUILTINS, defaultDashboards, formatValue, placeWidget, queryFor, removeWidget, toOption } from '../pages/dashboard/dashboardLogic'
import type { Dashboard } from '../api/stats'

describe('dashboard model', () => {
  test('default dashboard has every built-in, without overlaps', () => {
    const d = defaultDashboards().dashboards[0]
    expect(d.widgets.map((w) => w.type)).toEqual(BUILTINS.map((b) => b.type))
    for (const a of d.widgets) {
      for (const b of d.widgets) {
        if (a === b) continue
        const overlap =
          a.layout.x < b.layout.x + b.layout.w && a.layout.x + a.layout.w > b.layout.x && a.layout.y < b.layout.y + b.layout.h && a.layout.y + a.layout.h > b.layout.y
        expect(overlap).toBe(false)
      }
      expect(a.layout.x + a.layout.w).toBeLessThanOrEqual(12)
    }
  })

  test('placeWidget finds the first free spot', () => {
    expect(placeWidget([], 4, 3)).toEqual({ x: 0, y: 0, w: 4, h: 3 })
    const taken = [{ id: 'a', type: 'system', title: '', range: '1h' as const, layout: { x: 0, y: 0, w: 8, h: 2 } }]
    expect(placeWidget(taken, 4, 2)).toEqual({ x: 8, y: 0, w: 4, h: 2 })
    expect(placeWidget(taken, 6, 2)).toEqual({ x: 0, y: 2, w: 6, h: 2 })
  })

  test('add, remove, layout, custom queries', () => {
    let d: Dashboard = { id: 'x', name: 'X', widgets: [] }
    d = addBuiltin(d, 'top-talkers')
    d = addCustom(d, 'DNS by client', { group_by: ['src_ip'], filters: { kind: 'dns' } }, 'pie', '24h')
    expect(d.widgets).toHaveLength(2)
    expect(queryFor(d.widgets[0])).toMatchObject({ group_by: ['src_ip'], metric: 'bytes', from: '-1h' })
    expect(queryFor(d.widgets[1])).toEqual({ group_by: ['src_ip'], filters: { kind: 'dns' }, from: '-24h' })
    d = applyLayout(d, [{ i: d.widgets[0].id, x: 4, y: 1, w: 3, h: 3 }])
    expect(d.widgets[0].layout).toEqual({ x: 4, y: 1, w: 3, h: 3 })
    d = removeWidget(d, d.widgets[0].id)
    expect(d.widgets.map((w) => w.type)).toEqual(['custom'])
    expect(addBuiltin(d, 'nope')).toBe(d)
  })

  test('formatValue', () => {
    expect(formatValue('bytes', 1536)).toBe('1.5 KB')
    expect(formatValue('bytes', 5 * 1024 ** 3)).toBe('5.0 GB')
    expect(formatValue('count', 12345)).toBe((12345).toLocaleString())
  })

  test('toOption: grouped bars and time series', () => {
    const bars = toOption({ columns: ['src_ip', 'bytes'], rows: [['10.0.0.1', 900], ['10.0.0.2', 100]] }, 'bar')
    expect(bars.yAxis.data).toEqual(['10.0.0.1', '10.0.0.2'])
    expect(bars.series[0].data).toEqual([900, 100])

    const series = toOption({ columns: ['time', 'severity', 'count'], rows: [[60, '1', 2], [60, '2', 5], [120, '2', 1]] }, 'stacked')
    expect(series.series.map((s: { name: string }) => s.name)).toEqual(['1', '2'])
    expect(series.series[0].data).toEqual([2, 0])
    expect(series.series[1].data).toEqual([5, 1])
    expect(series.series[1].stack).toBe('total')

    const pie = toOption({ columns: ['action', 'count'], rows: [['deny', 3]] }, 'pie')
    expect(pie.series[0].data).toEqual([{ name: 'deny', value: 3 }])
  })
})
