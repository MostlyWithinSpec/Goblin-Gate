// Static routes (config "routes"): an IPv4 subnet through a gateway. Unlike
// the other object pages, a route has no name (its destination is its
// identity), so this page edits the list directly instead of via ConfigList.
import { useEffect, useState, type FormEvent } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { getCandidate, patchSection, type Candidate, type Route } from '../../api/objects'
import { EditDrawer } from '../../components/EditDrawer'
import { inputClass } from '../../components/ConfigList'
import { validateRoute, type Errors } from './networkLogic'

export default function StaticRoutes() {
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [routes, setRoutes] = useState<Route[]>([])
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ route: Route; index: number | null } | null>(null)
  const [errors, setErrors] = useState<Errors>({})

  useEffect(() => {
    getCandidate()
      .then((c) => {
        setCfg(c)
        setRoutes(c.routes ?? [])
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const save = async (next: Route[]) => {
    const prev = routes
    setRoutes(next)
    setError(null)
    try {
      await patchSection('routes', next)
    } catch (e) {
      setRoutes(prev)
      setError((e as Error).message)
    }
  }

  const clean = (r: Route): Route => {
    const out: Route = { destination: r.destination.trim(), gateway: r.gateway }
    if (r.comment?.trim()) out.comment = r.comment.trim()
    return out
  }

  const submit = (ev: FormEvent) => {
    ev.preventDefault()
    if (!editing || !cfg) return
    const r = clean(editing.route)
    const errs = validateRoute(r, cfg)
    if (!errs.destination && routes.some((x, i) => i !== editing.index && x.destination === r.destination))
      errs.destination = 'This destination already has a route'
    setErrors(errs)
    if (Object.keys(errs).length) return
    save(editing.index === null ? [...routes, r] : routes.map((x, i) => (i === editing.index ? r : x)))
    setEditing(null)
  }

  const open = (route: Route, index: number | null) => {
    setErrors({})
    setEditing({ route: structuredClone(route), index })
  }
  const set = (patch: Partial<Route>) => setEditing((e) => (e ? { ...e, route: { ...e.route, ...patch } } : e))
  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>

  const gateways = (cfg?.gateways ?? []).map((g) => g.name)

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">Static Routes</h2>
        <button
          type="button"
          disabled={!cfg}
          onClick={() => cfg && open({ destination: '', gateway: gateways[0] ?? '' }, null)}
          className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Plus size={16} /> New route
        </button>
      </div>
      <p className="mb-3 text-sm text-gray-600 dark:text-gray-400">
        Send a subnet through a specific gateway instead of the default route.
      </p>
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      <div className="overflow-x-auto">
        <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2">Destination</th>
              <th className="px-3 py-2">Gateway</th>
              <th className="px-3 py-2">Comment</th>
              <th className="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            {routes.map((r, i) => (
              <tr key={r.destination + i} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-1">
                  <button type="button" className="text-blue-600 hover:underline" onClick={() => open(r, i)}>
                    <code>{r.destination}</code>
                  </button>
                </td>
                <td className="px-3 py-1">{r.gateway}</td>
                <td className="px-3 py-1">{r.comment}</td>
                <td className="whitespace-nowrap px-3 py-1 text-right">
                  <button type="button" aria-label={`Delete ${r.destination}`} onClick={() => save(routes.filter((_, j) => j !== i))} className="ml-2 text-red-600">
                    <Trash2 size={16} />
                  </button>
                </td>
              </tr>
            ))}
            {cfg && routes.length === 0 && (
              <tr><td colSpan={4} className="px-3 py-6 text-center text-gray-500">Nothing configured yet.</td></tr>
            )}
            {!cfg && !error && (
              <tr><td colSpan={4} className="px-3 py-6 text-center text-gray-500">Loading…</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {editing && cfg && (
        <EditDrawer isOpen onClose={() => setEditing(null)} title={editing.index === null ? 'New route' : `Edit ${editing.route.destination}`}>
          <form onSubmit={submit} noValidate className="space-y-3 text-sm text-gray-700 dark:text-gray-300">
            <label className="block">
              Destination
              <input aria-label="Destination" className={inputClass} placeholder="192.168.2.0/24" value={editing.route.destination} onChange={(e) => set({ destination: e.target.value })} />
              {err('destination')}
            </label>
            <label className="block">
              Gateway
              <select aria-label="Gateway" className={inputClass} value={editing.route.gateway} onChange={(e) => set({ gateway: e.target.value })}>
                <option value="">Pick a gateway</option>
                {gateways.map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
              {err('gateway')}
            </label>
            <label className="block">
              Comment
              <input aria-label="Comment" className={inputClass} value={editing.route.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            </label>
            <div className="flex justify-end gap-2 pt-2">
              <button type="button" onClick={() => setEditing(null)} className="rounded border border-gray-300 px-3 py-1 dark:border-gray-600 dark:text-gray-200">Cancel</button>
              <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-white">Save</button>
            </div>
          </form>
        </EditDrawer>
      )}
    </div>
  )
}
