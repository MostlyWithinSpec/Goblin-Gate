import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Responsive, WidthProvider, type Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import { Plus, Trash2 } from 'lucide-react'
import { getDashboards, saveDashboards, type Dashboard, type Dashboards } from '../../api/stats'
import { Button } from '../../components'
import { addBuiltin, applyLayout, BUILTINS, defaultDashboards, newId, removeWidget, updateWidget } from './dashboardLogic'
import WidgetView from './WidgetView'

const Grid = WidthProvider(Responsive)

// DashboardPage shows named dashboards of draggable, resizable widgets,
// saved per user through /api/v1/ui/dashboards (task 025).
export default function DashboardPage() {
  const [docs, setDocs] = useState<Dashboards | null>(null)
  const [active, setActive] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const loaded = useRef(false)

  useEffect(() => {
    getDashboards()
      .then((d) => setDocs(d.dashboards.length ? d : defaultDashboards()))
      .catch((e: Error) => {
        setError(e.message)
        setDocs(defaultDashboards())
      })
  }, [])

  // save a moment after every change (drags fire many layout updates)
  useEffect(() => {
    if (!docs) return
    if (!loaded.current) {
      loaded.current = true
      return
    }
    const t = setTimeout(() => {
      saveDashboards(docs).then(() => setSaveError(null)).catch((e: Error) => setSaveError(e.message))
    }, 800)
    return () => clearTimeout(t)
  }, [docs])

  const edit = useCallback(
    (fn: (d: Dashboard) => Dashboard) =>
      setDocs((cur) => (cur ? { ...cur, dashboards: cur.dashboards.map((d, i) => (i === active ? fn(d) : d)) } : cur)),
    [active]
  )

  const dash = docs?.dashboards[active]
  const layout = useMemo(() => (dash?.widgets ?? []).map((w) => ({ i: w.id, ...w.layout, minW: 2, minH: 2 })), [dash])

  if (!docs || !dash) return <p className="p-6 text-gray-500" data-testid="dashboard-loading">Loading dashboards…</p>

  const addDashboard = () => {
    const name = window.prompt('Name of the new dashboard', `Dashboard ${docs.dashboards.length + 1}`)
    if (!name) return
    setDocs({ ...docs, dashboards: [...docs.dashboards, { id: newId('d'), name, widgets: [] }] })
    setActive(docs.dashboards.length)
  }
  const deleteDashboard = () => {
    if (docs.dashboards.length < 2 || !window.confirm(`Delete dashboard "${dash.name}"?`)) return
    setDocs({ ...docs, dashboards: docs.dashboards.filter((_, i) => i !== active) })
    setActive(0)
  }
  const rename = (name: string) => edit((d) => ({ ...d, name }))

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div role="tablist" className="flex flex-wrap gap-1">
          {docs.dashboards.map((d, i) => (
            <button
              key={d.id}
              role="tab"
              aria-selected={i === active}
              className={`rounded-t px-3 py-1 text-sm ${i === active ? 'bg-blue-600 text-white' : 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200'}`}
              onClick={() => setActive(i)}
              onDoubleClick={() => {
                const n = window.prompt('Rename dashboard', d.name)
                if (n) rename(n)
              }}
            >
              {d.name}
            </button>
          ))}
        </div>
        <Button size="sm" variant="ghost" onClick={addDashboard} aria-label="New dashboard"><Plus size={14} /> Dashboard</Button>
        <div className="flex-1" />
        {saveError && <span className="text-xs text-red-600">Not saved: {saveError}</span>}
        <div className="relative">
          <Button size="sm" variant="primary" onClick={() => setAdding((a) => !a)}><Plus size={14} /> Add widget</Button>
          {adding && (
            <ul role="menu" className="absolute right-0 z-20 mt-1 w-56 rounded border border-gray-200 bg-white py-1 text-sm shadow-lg dark:border-gray-600 dark:bg-gray-800">
              {BUILTINS.map((b) => (
                <li key={b.type}>
                  <button
                    role="menuitem"
                    className="block w-full px-3 py-1 text-left text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
                    onClick={() => {
                      edit((d) => addBuiltin(d, b.type))
                      setAdding(false)
                    }}
                  >
                    {b.title}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        {docs.dashboards.length > 1 && (
          <Button size="sm" variant="outline" onClick={deleteDashboard} aria-label="Delete dashboard"><Trash2 size={14} /></Button>
        )}
      </div>
      {error && <p className="mb-2 text-sm text-yellow-700">Saved dashboards could not be loaded ({error}); showing the default.</p>}
      {dash.widgets.length === 0 ? (
        <p className="rounded border border-dashed border-gray-300 p-8 text-center text-gray-500">This dashboard is empty. Use "Add widget".</p>
      ) : (
        <Grid
          className="layout"
          layouts={{ lg: layout }}
          breakpoints={{ lg: 1024, sm: 0 }}
          cols={{ lg: 12, sm: 1 }}
          rowHeight={70}
          draggableHandle=".widget-drag"
          onLayoutChange={(cur: Layout[], all: Record<string, Layout[]>) => {
            // only the 12-column layout is stored; the phone layout stacks
            if (all.lg) edit((d) => applyLayout(d, all.lg))
            void cur
          }}
        >
          {dash.widgets.map((w) => (
            <div key={w.id}>
              <WidgetView widget={w} onChange={(p) => edit((d) => updateWidget(d, w.id, p))} onRemove={() => edit((d) => removeWidget(d, w.id))} />
            </div>
          ))}
        </Grid>
      )}
    </div>
  )
}
