import { useEffect, useRef } from 'react'
import * as echarts from 'echarts'

// Chart renders an ECharts option and resizes with its container. onClick
// gets the clicked category/series name.
export default function Chart({ option, dark, onClick }: { option: Record<string, unknown>; dark?: boolean; onClick?: (name: string) => void }) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    if (!el.current) return
    const c = echarts.init(el.current, dark ? 'dark' : undefined, { renderer: 'canvas' })
    chart.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current)
    return () => {
      ro.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [dark])

  useEffect(() => {
    chart.current?.setOption({ backgroundColor: 'transparent', ...option }, true)
  }, [option, dark])

  useEffect(() => {
    const c = chart.current
    if (!c || !onClick) return
    const handler = (p: { name?: string; seriesName?: string }) => onClick(p.name || p.seriesName || '')
    c.on('click', handler)
    return () => {
      c.off('click', handler)
    }
  }, [onClick, dark])

  return <div ref={el} className="h-full w-full" data-testid="chart" />
}
