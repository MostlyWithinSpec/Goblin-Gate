import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2, ArrowUp, ArrowDown, Pencil, GripVertical } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { EditDrawer } from '../../../components/EditDrawer'
import SentryNav from './SentryNav'
import { getRules, putRules, isPluginNotRunning, type Rule } from '../../../api/sentry'

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'
const NAME_RE = /^[A-Za-z0-9_.-]{1,63}$/

const emptyRule = (): Rule => ({
  id: 0,
  name: '',
  enabled: true,
  match: {},
  action: { tool: 'block', minutes: 60 }
})

export default function Rules() {
  const qc = useQueryClient()
  const [editing, setEditing] = useState<Rule | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [error, setError] = useState<string[] | null>(null)

  const q = useQuery({ queryKey: ['sentry', 'rules'], queryFn: getRules, retry: false })

  const mut = useMutation({
    mutationFn: (rules: Rule[]) => putRules(rules),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sentry'] })
      setEditing(null)
      setError(null)
    },
    onError: (e) => {
      const problems = (e as { problems?: string[] }).problems
      setError(problems ?? [`${(e as Error).message}`])
    }
  })

  if (q.error && isPluginNotRunning(q.error)) {
    return (
      <div>
        <SentryNav />
        <p className="mt-4 text-sm text-gray-500">Goblin Sentry is not running.</p>
      </div>
    )
  }

  const rules = q.data?.rules ?? []

  const save = (updated: Rule[]) => {
    mut.mutate(updated)
  }

  const startNew = () => {
    setEditing(emptyRule())
    setIsNew(true)
    setError(null)
  }

  const startEdit = (r: Rule) => {
    setEditing({ ...r, match: { ...r.match }, action: { ...r.action } })
    setIsNew(false)
    setError(null)
  }

  const commitEdit = () => {
    if (!editing) return
    if (!NAME_RE.test(editing.name)) {
      setError(['name: must be 1-63 characters of [A-Za-z0-9_.-]'])
      return
    }
    const next = isNew ? [...rules, editing] : rules.map((r) => (r.id === editing.id ? editing : r))
    save(next)
  }

  const move = (idx: number, dir: -1 | 1) => {
    const j = idx + dir
    if (j < 0 || j >= rules.length) return
    const next = [...rules]
    ;[next[idx], next[j]] = [next[j], next[idx]]
    save(next)
  }

  const remove = (idx: number) => {
    const r = rules[idx]
    if (!window.confirm(`Delete rule "${r.name}"?`)) return
    save(rules.filter((_, i) => i !== idx))
  }

  const toggleEnabled = (idx: number) => {
    const next = rules.map((r, i) => (i === idx ? { ...r, enabled: !r.enabled } : r))
    save(next)
  }

  return (
    <div>
      <SentryNav />

      <div className="mt-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Rules</h2>
        <button
          type="button"
          onClick={startNew}
          className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700"
        >
          <Plus size={14} /> Add rule
        </button>
      </div>

      {q.isLoading && <p className="mt-4 text-sm text-gray-500">Loading…</p>}
      {q.isError && (
        <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
          {(q.error as Error).message}
        </div>
      )}

      {rules.length === 0 && !q.isLoading && (
        <p className="mt-4 text-sm text-gray-500">No rules yet. Add one to automate responses.</p>
      )}

      <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-800">
            <tr>
              <th className="w-10 px-2 py-2" />
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">#</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Name</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Match</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Action</th>
              <th className="px-3 py-2 text-center text-xs font-medium uppercase text-gray-500">Enabled</th>
              <th className="w-28 px-2 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-700/50 dark:bg-gray-800">
            {rules.map((r, i) => (
              <tr key={r.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/40">
                <td className="px-2 py-2 text-gray-300"><GripVertical size={14} /></td>
                <td className="px-3 py-2 text-sm text-gray-500">{i + 1}</td>
                <td className="px-3 py-2 text-sm font-medium">
                  {r.name}
                  {r.builtin && <span className="ml-1 text-xs text-gray-400">(builtin)</span>}
                </td>
                <td className="max-w-xs truncate px-3 py-2 text-xs text-gray-500" title={JSON.stringify(r.match)}>
                  {Object.entries(r.match).map(([k, v]) => `${k}=${v}`).join(', ') || 'any'}
                </td>
                <td className="px-3 py-2 text-sm text-gray-700 dark:text-gray-300">
                  {r.action.tool}
                  {r.action.minutes != null && ` ${r.action.minutes}m`}
                </td>
                <td className="px-3 py-2 text-center">
                  <button
                    type="button"
                    onClick={() => toggleEnabled(i)}
                    className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      r.enabled
                        ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                        : 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400'
                    }`}
                  >
                    {r.enabled ? 'on' : 'off'}
                  </button>
                </td>
                <td className="px-2 py-2">
                  <div className="flex items-center justify-end gap-1">
                    <button type="button" onClick={() => move(i, -1)} disabled={i === 0} className="rounded p-1 text-gray-400 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-700" title="Move up"><ArrowUp size={14} /></button>
                    <button type="button" onClick={() => move(i, 1)} disabled={i === rules.length - 1} className="rounded p-1 text-gray-400 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-700" title="Move down"><ArrowDown size={14} /></button>
                    <button type="button" onClick={() => startEdit(r)} className="rounded p-1 text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-900/30" title="Edit"><Pencil size={14} /></button>
                    <button type="button" onClick={() => remove(i)} className="rounded p-1 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30" title="Delete"><Trash2 size={14} /></button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <EditDrawer isOpen={!!editing} onClose={() => setEditing(null)} title={isNew ? 'Add rule' : `Edit rule: ${editing?.name}`}>
        {editing && (
          <div className="space-y-4">
            {error && (
              <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-300">
                {error.map((p) => (<div key={p}>{p}</div>))}
              </div>
            )}

            <label className="block text-sm">
              Name
              <input type="text" value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} className={inputClass} />
            </label>

            <div>
              <p className="mb-1 text-xs font-medium uppercase text-gray-500">Match (all must match)</p>
              <div className="space-y-2">
                <label className="block text-sm">Signature (regex)
                  <input type="text" value={editing.match.signature ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, signature: e.target.value || undefined } })} className={inputClass} />
                </label>
                <label className="block text-sm">Signature ID
                  <input type="text" inputMode="numeric" value={editing.match.signature_id ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, signature_id: e.target.value.trim() === '' ? undefined : e.target.value.trim() } })} className={inputClass} />
                </label>
                <label className="block text-sm">Category
                  <input type="text" value={editing.match.category ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, category: e.target.value || undefined } })} className={inputClass} />
                </label>
                <label className="block text-sm">Max severity (1=high … 4=low)
                  <select value={editing.match.max_severity ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, max_severity: e.target.value ? Number(e.target.value) : undefined } })} className={inputClass}>
                    <option value="">any</option>
                    <option value="1">high only</option>
                    <option value="2">high + medium</option>
                    <option value="3">high + medium + low</option>
                    <option value="4">all</option>
                  </select>
                </label>
                <label className="block text-sm">Direction
                  <select value={editing.match.direction ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, direction: e.target.value || undefined } })} className={inputClass}>
                    <option value="">any</option>
                    <option value="inbound">inbound</option>
                    <option value="outbound">outbound</option>
                    <option value="lateral">lateral</option>
                    <option value="external">external</option>
                  </select>
                </label>
                <label className="block text-sm">Remote (regex)
                  <input type="text" value={editing.match.remote ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, remote: e.target.value || undefined } })} className={inputClass} />
                </label>
                <label className="block text-sm">Local (regex)
                  <input type="text" value={editing.match.local ?? ''} onChange={(e) => setEditing({ ...editing, match: { ...editing.match, local: e.target.value || undefined } })} className={inputClass} />
                </label>
              </div>
            </div>

            <div>
              <p className="mb-1 text-xs font-medium uppercase text-gray-500">Action</p>
              <div className="space-y-2">
                <label className="block text-sm">Tool
                  <select value={editing.action.tool} onChange={(e) => setEditing({ ...editing, action: { ...editing.action, tool: e.target.value } })} className={inputClass}>
                    <option value="block">block</option>
                    <option value="ignore">ignore</option>
                  </select>
                </label>
                {editing.action.tool === 'block' && (
                  <label className="block text-sm">Minutes (1–10080)
                    <input type="number" min={1} max={10080} value={editing.action.minutes ?? 60} onChange={(e) => setEditing({ ...editing, action: { ...editing.action, minutes: e.target.value === '' ? undefined : Number(e.target.value) } })} className={inputClass} />
                  </label>
                )}
              </div>
            </div>

            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={editing.enabled} onChange={(e) => setEditing({ ...editing, enabled: e.target.checked })} />
              Enabled
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={!!editing.auto} onChange={(e) => setEditing({ ...editing, auto: e.target.checked || undefined })} />
              Auto (apply without approval in approve mode)
            </label>

            <label className="block text-sm">
              Comment
              <textarea value={editing.comment ?? ''} onChange={(e) => setEditing({ ...editing, comment: e.target.value || undefined })} rows={2} className={inputClass} />
            </label>

            <div className="flex justify-end gap-2 border-t border-gray-200 pt-4 dark:border-gray-700">
              <button type="button" onClick={() => setEditing(null)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">Cancel</button>
              <button
                type="button"
                disabled={mut.isPending}
                onClick={commitEdit}
                className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {mut.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        )}
      </EditDrawer>
    </div>
  )
}
