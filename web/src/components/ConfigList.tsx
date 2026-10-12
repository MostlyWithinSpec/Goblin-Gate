// ConfigList: a table of one named list in the candidate config with a
// drawer form to add/edit entries; every change PATCHes the whole list
// (path), and the server's validation errors for that list are shown. The
// page supplies the columns, an empty entry, the form and a validator.
import React, { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { getCandidate, patchSection, type Candidate } from '../api/objects'
import type { ValidationError } from '../api/policy'
import { EditDrawer } from './EditDrawer'

export type Errors = Record<string, string>

export interface Column<T> {
  label: string
  render: (item: T) => React.ReactNode
}

export interface FormCtx {
  cfg: Candidate
  errors: Errors
  isNew: boolean
  // err renders the message for one field, if any
  err: (field: string) => React.ReactNode
}

interface Props<T extends { name: string }> {
  title: string
  path: string // e.g. "addresses", "profiles.webfilter"
  select: (cfg: Candidate) => T[] | undefined
  columns: Column<T>[]
  empty: (cfg: Candidate) => T
  form: (item: T, set: (patch: Partial<T>) => void, ctx: FormCtx) => React.ReactNode
  validate?: (item: T, cfg: Candidate) => Errors
  clean?: (item: T) => T // drop empty optional fields before saving
  newLabel?: string
  intro?: React.ReactNode
  // after: rendered below the table with the loaded candidate (live state etc.)
  after?: (cfg: Candidate, items: T[]) => React.ReactNode
  rowActions?: (item: T) => React.ReactNode
  wide?: boolean // wider drawer content (big forms)
}

export const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'
export const NAME_RE = /^[A-Za-z0-9_.-]{1,63}$/ // config.nameRe

export function ConfigList<T extends { name: string }>(p: Props<T>) {
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [items, setItems] = useState<T[]>([])
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [editing, setEditing] = useState<{ item: T; index: number | null } | null>(null)
  const [errors, setErrors] = useState<Errors>({})

  useEffect(() => {
    getCandidate()
      .then((c) => {
        setCfg(c)
        setItems(p.select(c) ?? [])
      })
      .catch((e: Error) => setError(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = async (next: T[]) => {
    const prev = items
    setItems(next)
    setError(null)
    try {
      const res = await patchSection(p.path, next)
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path === p.path || v.path.startsWith(p.path + '[') || v.path.startsWith(p.path + '.')))
    } catch (e) {
      setItems(prev)
      setError((e as Error).message)
    }
  }

  const submit = (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!editing || !cfg) return
    const item = p.clean ? p.clean(editing.item) : editing.item
    const errs: Errors = { ...(p.validate ? p.validate(item, cfg) : {}) }
    if (!NAME_RE.test(item.name)) errs.name ??= 'Use 1-63 letters, digits, ".", "_" or "-"'
    else if (items.some((x, i) => i !== editing.index && x.name === item.name)) errs.name = 'Name already used'
    setErrors(errs)
    if (Object.keys(errs).length) return
    save(editing.index === null ? [...items, item] : items.map((x, i) => (i === editing.index ? item : x)))
    setEditing(null)
  }

  const open = (item: T, index: number | null) => {
    setErrors({})
    setEditing({ item: structuredClone(item), index })
  }
  const set = (patch: Partial<T>) => setEditing((e) => (e ? { ...e, item: { ...e.item, ...patch } } : e))
  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">{p.title}</h2>
        <button
          type="button"
          disabled={!cfg}
          onClick={() => cfg && open(p.empty(cfg), null)}
          className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Plus size={16} /> {p.newLabel ?? 'New'}
        </button>
      </div>
      {p.intro && <div className="mb-3 text-sm text-gray-600 dark:text-gray-400">{p.intro}</div>}
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      {serverErrors.length > 0 && (
        <ul className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800">
          {serverErrors.map((v, i) => <li key={i}><code>{v.path}</code>: {v.message}</li>)}
        </ul>
      )}
      <div className="overflow-x-auto">
        <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2">Name</th>
              {p.columns.map((c) => <th key={c.label} className="px-3 py-2">{c.label}</th>)}
              <th className="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            {items.map((x, i) => (
              <tr key={x.name + i} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-1">
                  <button type="button" className="text-blue-600 hover:underline" onClick={() => open(x, i)}>{x.name}</button>
                </td>
                {p.columns.map((c) => <td key={c.label} className="px-3 py-1">{c.render(x)}</td>)}
                <td className="whitespace-nowrap px-3 py-1 text-right">
                  {p.rowActions?.(x)}
                  <button type="button" aria-label={`Delete ${x.name}`} onClick={() => save(items.filter((_, j) => j !== i))} className="ml-2 text-red-600">
                    <Trash2 size={16} />
                  </button>
                </td>
              </tr>
            ))}
            {cfg && items.length === 0 && (
              <tr><td colSpan={p.columns.length + 2} className="px-3 py-6 text-center text-gray-500">Nothing configured yet.</td></tr>
            )}
            {!cfg && !error && (
              <tr><td colSpan={p.columns.length + 2} className="px-3 py-6 text-center text-gray-500">Loading…</td></tr>
            )}
          </tbody>
        </table>
      </div>
      {cfg && p.after?.(cfg, items)}

      {editing && cfg && (
        <EditDrawer isOpen onClose={() => setEditing(null)} title={editing.index === null ? (p.newLabel ?? 'New') : `Edit ${editing.item.name}`}>
          <form onSubmit={submit} noValidate className="space-y-3 text-sm text-gray-700 dark:text-gray-300">
            <label className="block">
              Name
              <input aria-label="Name" className={inputClass} value={editing.item.name} onChange={(e) => set({ name: e.target.value } as Partial<T>)} />
              {err('name')}
            </label>
            {p.form(editing.item, set, { cfg, errors, isNew: editing.index === null, err })}
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

// Small form helpers shared by the pages ------------------------------------

export function Checklist({ label, options, value, onChange }: { label: string; options: string[]; value: string[]; onChange: (v: string[]) => void }) {
  return (
    <fieldset className="block">
      <legend>{label}</legend>
      {options.length === 0 && <p className="text-xs text-gray-500">None defined yet.</p>}
      <div className="mt-1 max-h-48 overflow-y-auto rounded border border-gray-200 p-2 dark:border-gray-600">
        {options.map((o) => (
          <label key={o} className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={value.includes(o)}
              onChange={(e) => onChange(e.target.checked ? [...value, o] : value.filter((x) => x !== o))}
            />
            {o}
          </label>
        ))}
      </div>
    </fieldset>
  )
}

// ListInput edits a string list as one entry per line (domains, CIDRs).
export function ListInput({ label, value, onChange, placeholder }: { label: string; value?: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [text, setText] = useState((value ?? []).join('\n'))
  return (
    <label className="block">
      {label}
      <textarea
        aria-label={label}
        rows={4}
        placeholder={placeholder}
        className={inputClass + ' font-mono text-xs'}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          onChange(e.target.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean))
        }}
      />
    </label>
  )
}

// omitEmpty removes "", [] and {} fields so optional values stay unset.
export function omitEmpty<T extends object>(x: T): T {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(x)) {
    if (v === '' || v === undefined || v === null) continue
    if (Array.isArray(v) && v.length === 0) continue
    if (typeof v === 'object' && !Array.isArray(v) && Object.keys(v as object).length === 0) continue
    out[k] = v
  }
  return out as T
}
