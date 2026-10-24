import React, { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { getNetworkConfig, patchSection, wanInterfaces, type PortForward } from '../../api/network'
import type { ValidationError } from '../../api/policy'
import { EditDrawer } from '../../components/EditDrawer'
import { EditParam } from '../../components/ConfigList'
import { useInRouterContext } from 'react-router-dom'
import { validatePortForward, type Errors } from './networkLogic'

const empty = (wan: string[]): PortForward => ({ name: '', in_intf: wan[0] ?? '', ext_port: '', protocol: 'tcp', to_addr: '', to_port: '' })

export default function PortForwards() {
  const [items, setItems] = useState<PortForward[]>([])
  const [wan, setWan] = useState<string[]>([])
  // address objects and groups a forward's source may name (countries too)
  const [sources, setSources] = useState<{ name: string; kind: string }[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [editing, setEditing] = useState<{ pf: PortForward; index: number | null } | null>(null)
  const [errors, setErrors] = useState<Errors>({})
  const inRouter = useInRouterContext()

  useEffect(() => {
    getNetworkConfig()
      .then((cfg) => {
        setItems(cfg.nat?.port_forwards ?? [])
        setWan(wanInterfaces(cfg))
        setSources([
          ...(cfg.addresses ?? []).map((a) => ({ name: a.name, kind: a.type })),
          ...(cfg.address_groups ?? []).map((g) => ({ name: g.name, kind: 'group' })),
        ])
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const save = async (next: PortForward[]): Promise<boolean> => {
    const prev = items
    setItems(next)
    setError(null)
    try {
      const res = await patchSection('nat.port_forwards', next)
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('nat.port_forwards')))
      return true
    } catch (e) {
      setItems(prev)
      setError((e as Error).message)
      return false
    }
  }

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!editing) return
    const others = items.filter((_, i) => i !== editing.index)
    const errs = validatePortForward(editing.pf, wan, others)
    setErrors(errs)
    if (Object.keys(errs).length) return
    const pf: PortForward = { ...editing.pf, ext_port: editing.pf.ext_port.trim(), to_port: editing.pf.to_port.trim(), to_addr: editing.pf.to_addr.trim() }
    if (!pf.source?.length) delete pf.source
    // the drawer stays open when the save fails, so the edits are not lost
    if (await save(editing.index === null ? [...items, pf] : items.map((x, i) => (i === editing.index ? pf : x)))) setEditing(null)
  }

  const set = <K extends keyof PortForward>(k: K, v: PortForward[K]) =>
    setEditing((e) => (e ? { ...e, pf: { ...e.pf, [k]: v } } : e))
  const input = 'mt-1 block min-h-[40px] w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'
  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">Virtual IPs / Port Forwards</h2>
        <button
          type="button"
          disabled={!loaded}
          onClick={() => {
            setErrors({})
            setEditing({ pf: empty(wan), index: null })
          }}
          className="flex min-h-[40px] items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Plus size={16} /> New port forward
        </button>
      </div>
      {inRouter && loaded && (
        <EditParam
          onEdit={(name) => {
            const i = items.findIndex((x) => x.name === name)
            if (i >= 0) {
              setErrors({})
              setEditing({ pf: items[i], index: i })
            }
          }}
        />
      )}
      {error && !editing && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      {serverErrors.length > 0 && (
        <ul className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {serverErrors.map((v) => <li key={v.path}><code>{v.path}</code>: {v.message}</li>)}
        </ul>
      )}
      <div className="max-w-full overflow-x-auto rounded-lg" data-testid="table-scroll">
      <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
        <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
          <tr>
            <th className="sticky left-0 z-10 bg-gray-50 px-3 py-2 dark:bg-gray-700">Name</th><th className="px-3 py-2">Interface</th><th className="px-3 py-2">External</th>
            <th className="px-3 py-2">Protocol</th><th className="px-3 py-2">Internal</th><th className="px-3 py-2">From</th><th className="px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {items.map((pf, i) => (
            <tr key={pf.name} className="border-t border-gray-100 dark:border-gray-700">
              <td className="sticky left-0 z-10 bg-white px-3 py-1 dark:bg-gray-800">
                <button type="button" className="min-h-[40px] text-left text-blue-600 hover:underline" onClick={() => { setErrors({}); setEditing({ pf, index: i }) }}>
                  {pf.name}
                </button>
              </td>
              <td className="px-3 py-1">{pf.in_intf}</td>
              <td className="px-3 py-1">{pf.ext_port}</td>
              <td className="px-3 py-1 uppercase">{pf.protocol}</td>
              <td className="px-3 py-1">{pf.to_addr}:{pf.to_port || pf.ext_port}</td>
              <td className="px-3 py-1">{pf.source?.length ? pf.source.join(', ') : <span className="text-gray-500">anyone</span>}</td>
              <td className="px-3 py-1 text-right">
                <button type="button" aria-label={`Delete ${pf.name}`} onClick={() => window.confirm(`Delete port forward "${pf.name}"?`) && save(items.filter((_, j) => j !== i))} className="text-red-600">
                  <Trash2 size={16} />
                </button>
              </td>
            </tr>
          ))}
          {loaded && items.length === 0 && (
            <tr><td colSpan={7} className="px-3 py-6 text-center text-gray-500">No port forwards.</td></tr>
          )}
        </tbody>
      </table>
      </div>

      {editing && (
        <EditDrawer isOpen onClose={() => setEditing(null)} title={editing.index === null ? 'New port forward' : `Edit ${editing.pf.name}`}>
          <form onSubmit={submit} noValidate className="space-y-3 text-sm text-gray-700 dark:text-gray-300">
            {error && <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
            <label className="block">Name<input aria-label="Name" className={input} value={editing.pf.name} onChange={(e) => set('name', e.target.value)} />{err('name')}</label>
            <label className="block">
              Incoming interface
              <select aria-label="Incoming interface" className={input} value={editing.pf.in_intf} onChange={(e) => set('in_intf', e.target.value)}>
                {wan.length === 0 && <option value="">no WAN interface</option>}
                {wan.map((w) => <option key={w} value={w}>{w}</option>)}
              </select>
              {err('in_intf')}
            </label>
            <label className="block">External port<input aria-label="External port" placeholder="443 or 8000-8100" className={input} value={editing.pf.ext_port} onChange={(e) => set('ext_port', e.target.value)} />{err('ext_port')}</label>
            <label className="block">
              Protocol
              <select aria-label="Protocol" className={input} value={editing.pf.protocol} onChange={(e) => set('protocol', e.target.value as PortForward['protocol'])}>
                <option value="tcp">TCP</option><option value="udp">UDP</option>
              </select>
            </label>
            <label className="block">Internal address<input aria-label="Internal address" placeholder="192.168.1.10" className={input} value={editing.pf.to_addr} onChange={(e) => set('to_addr', e.target.value)} />{err('to_addr')}</label>
            <label className="block">Internal port<input aria-label="Internal port" placeholder="same as external" className={input} value={editing.pf.to_port} onChange={(e) => set('to_port', e.target.value)} />{err('to_port')}</label>
            <fieldset>
              <legend>Allowed sources</legend>
              <p className="text-xs text-gray-500">Only these addresses may use the forward; none ticked lets anyone in. Country objects (Network → Addresses) limit it to some countries.</p>
              <div className="mt-1 max-h-40 overflow-y-auto rounded border border-gray-200 p-1 dark:border-gray-600">
                {sources.length === 0 && <p className="px-1 text-xs text-gray-500">No address objects yet.</p>}
                {sources.map((o) => {
                  const on = editing.pf.source?.includes(o.name) ?? false
                  return (
                    <label key={o.name} className="flex items-center gap-2 px-1 py-0.5">
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() => set('source', on ? (editing.pf.source ?? []).filter((n) => n !== o.name) : [...(editing.pf.source ?? []), o.name])}
                      />
                      {o.name} <span className="text-xs text-gray-500">{o.kind}</span>
                    </label>
                  )
                })}
              </div>
            </fieldset>
            <div className="flex justify-end gap-2 pt-2">
              <button type="button" onClick={() => setEditing(null)} className="rounded border border-gray-300 px-3 py-1">Cancel</button>
              <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-white">Save</button>
            </div>
          </form>
        </EditDrawer>
      )}
    </div>
  )
}
