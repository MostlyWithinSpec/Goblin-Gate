// The Roles section of System → Admins (task 150): create, change and delete
// custom admin roles. The built-in roles are shown read-only — the server
// refuses a change to them — and every role lists the users that have it.
// Scope labels come from the API (GET /api/v1/roles answers a description per
// scope); English is the catalog's text for the same ids, kept in step here.
import React, { useCallback, useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { createRole, deleteRole, getRoles, updateRole, type RoleInfo, type ScopeInfo } from '../api/roles'
import { NAME_RE, inputClass } from './ConfigList'
import { t } from '../i18n'

// The six scopes of internal/auth (tokens.go), in the order the API lists them.
const SCOPE_IDS = ['read', 'write:firewall', 'write:network', 'write:system', 'actions', 'commit'] as const

// A t() call must be a string literal, so the label is picked by scope id.
const scopeLabel = (id: string): string => {
  switch (id) {
    case 'read':
      return t('roles.scopes.read')
    case 'write:firewall':
      return t('roles.scopes.writeFirewall')
    case 'write:network':
      return t('roles.scopes.writeNetwork')
    case 'write:system':
      return t('roles.scopes.writeSystem')
    case 'actions':
      return t('roles.scopes.actions')
    case 'commit':
      return t('roles.scopes.commit')
    default:
      return id
  }
}

// scopeText prefers the server's description — the English catalog carries the
// same wording, so a translated UI still follows the server if it changes.
const scopeText = (scope: ScopeInfo | undefined, id: string): string =>
  scope && scope.description ? scope.description : scopeLabel(id)

// configCheck mirrors config.CheckRole: a name of 1-63 [A-Za-z0-9_.-] and no
// duplicate scope. The server answers 422 with the same messages for more.
function checkRoleForm(name: string, scopes: string[]): Record<string, string> {
  const errs: Record<string, string> = {}
  if (!NAME_RE.test(name)) errs.name = t('roles.nameInvalid')
  if (new Set(scopes).size !== scopes.length) errs.scopes = t('roles.duplicateScope')
  return errs
}

export const RolesSection: React.FC = () => {
  const [roles, setRoles] = useState<RoleInfo[] | null>(null)
  const [scopes, setScopes] = useState<ScopeInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [newScopes, setNewScopes] = useState<string[]>(['read'])
  const [editing, setEditing] = useState<string | null>(null)
  const [editDescription, setEditDescription] = useState('')
  const [editScopes, setEditScopes] = useState<string[]>([])
  const [formErrors, setFormErrors] = useState<Record<string, string>>({})

  const load = useCallback(() => {
    getRoles()
      .then((r) => {
        setRoles(r.roles)
        setScopes(r.scopes)
      })
      .catch((e: Error) => setError(e.message))
  }, [])
  useEffect(load, [load])

  const toggle = (list: string[], set: (v: string[]) => void, scope: string, on: boolean) =>
    set(on ? [...new Set([...list, scope])] : list.filter((s) => s !== scope))

  const submitAdd = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const errs = checkRoleForm(name, newScopes)
    if (roles?.some((r) => r.name === name)) errs.name = t('roles.nameTaken')
    setFormErrors(errs)
    if (Object.keys(errs).length) return
    try {
      await createRole({ name, description, scopes: orderScopes(newScopes) })
      setShowAdd(false)
      setName('')
      setDescription('')
      setNewScopes(['read'])
      setFormErrors({})
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const startEdit = (r: RoleInfo) => {
    setFormErrors({})
    setError(null)
    setEditing(r.name)
    setEditDescription(r.description)
    setEditScopes([...r.scopes])
  }

  const submitEdit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!editing) return
    const errs = checkRoleForm(editing, editScopes)
    setFormErrors(errs)
    if (Object.keys(errs).length) return
    try {
      await updateRole(editing, { description: editDescription, scopes: orderScopes(editScopes) })
      setEditing(null)
      setFormErrors({})
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const doDelete = async (r: RoleInfo) => {
    setError(null)
    if (!window.confirm(t('roles.deleteConfirm', { name: r.name }))) return
    try {
      await deleteRole(r.name)
      load()
    } catch (e) {
      // 409 role_conflict names the user that still has the role.
      setError((e as Error).message)
    }
  }

  if (!roles) return <p className="text-gray-500">{error ?? t('system.loading')}</p>

  return (
    <div className="mt-6 rounded-lg border border-gray-200 p-4 dark:border-gray-600">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-800 dark:text-white">{t('roles.title')}</h3>
        <button type="button" onClick={() => setShowAdd((s) => !s)} className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">
          <Plus size={16} /> {t('roles.newRole')}
        </button>
      </div>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">{t('roles.intro')}</p>
      {error && <div role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">{error}</div>}
      <table className="mt-3 min-w-full text-sm">
        <thead className="text-left text-xs uppercase text-gray-500">
          <tr>
            <th className="px-2 py-2">{t('roles.colName')}</th>
            <th className="px-2 py-2">{t('roles.colScopes')}</th>
            <th className="px-2 py-2">{t('roles.colUsers')}</th>
            <th className="px-2 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {roles.map((r) => (
            <tr key={r.name} className="border-t border-gray-100 dark:border-gray-700">
              <td className="px-2 py-2 align-top font-medium">
                {r.name}
                {r.builtin && <span className="ml-1 text-xs text-gray-400">{t('roles.builtinTag')}</span>}
                {r.description && <span className="block text-xs font-normal text-gray-500">{r.description}</span>}
              </td>
              <td className="px-2 py-2 align-top">
                <span className="flex flex-wrap gap-x-3 gap-y-1">
                  {r.scopes.map((s) => (
                    <span key={s} className="text-gray-600 dark:text-gray-300" title={s}>
                      {scopeText(scopes.find((x) => x.id === s), s)}
                    </span>
                  ))}
                </span>
              </td>
              <td className="px-2 py-2 align-top text-gray-600 dark:text-gray-300">
                {r.users.length ? r.users.join(', ') : t('roles.noUsers')}
              </td>
              <td className="whitespace-nowrap px-2 py-2 text-right align-top">
                {!r.builtin && (
                  <>
                    <button type="button" onClick={() => startEdit(r)} className="mr-2 text-sm text-blue-600 hover:underline">
                      {t('roles.edit')}
                    </button>
                    <button type="button" onClick={() => doDelete(r)} className="text-red-600" aria-label={`${t('roles.delete')} ${r.name}`}>
                      <Trash2 size={16} />
                    </button>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {showAdd && (
        <form onSubmit={submitAdd} noValidate className="mt-4 max-w-lg space-y-3 rounded border border-gray-200 p-4 dark:border-gray-600">
          <h4 className="text-sm font-semibold text-gray-800 dark:text-white">{t('roles.newRole')}</h4>
          <label className="block text-sm">
            {t('roles.colName')}
            <input aria-label={t('roles.colName')} className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
            {formErrors.name && <p role="alert" className="mt-1 text-xs text-red-600">{formErrors.name}</p>}
          </label>
          <label className="block text-sm">
            {t('roles.description')}
            <input aria-label={t('roles.description')} className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} />
          </label>
          <ScopeChecks scopes={scopes} value={newScopes} onChange={(v) => setNewScopes(v)} />
          {formErrors.scopes && <p role="alert" className="text-xs text-red-600">{formErrors.scopes}</p>}
          <div className="flex gap-2">
            <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-sm text-white">{t('system.create')}</button>
            <button type="button" onClick={() => { setShowAdd(false); setFormErrors({}) }} className="rounded border border-gray-300 px-3 py-1 text-sm">{t('common.cancel')}</button>
          </div>
        </form>
      )}

      {editing && (
        <form onSubmit={submitEdit} noValidate aria-label={t('roles.editRole', { name: editing })} className="mt-4 max-w-lg space-y-3 rounded border border-blue-200 p-4 dark:border-blue-800">
          <h4 className="text-sm font-semibold text-gray-800 dark:text-white">{t('roles.editRole', { name: editing })}</h4>
          <label className="block text-sm">
            {t('roles.description')}
            <input aria-label={t('roles.description')} className={inputClass} value={editDescription} onChange={(e) => setEditDescription(e.target.value)} />
          </label>
          <ScopeChecks scopes={scopes} value={editScopes} onChange={(v) => setEditScopes(v)} />
          {formErrors.scopes && <p role="alert" className="text-xs text-red-600">{formErrors.scopes}</p>}
          <p className="text-xs text-gray-500">{t('roles.editAppliesNote')}</p>
          <div className="flex gap-2">
            <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-sm text-white">{t('system.save')}</button>
            <button type="button" onClick={() => setEditing(null)} className="rounded border border-gray-300 px-3 py-1 text-sm">{t('common.cancel')}</button>
          </div>
        </form>
      )}
    </div>
  )
}

// orderScopes sends the scopes in the order the server lists them, so a saved
// role reads the same way every time.
function orderScopes(scopes: string[]): string[] {
  const known: string[] = []
  for (const id of SCOPE_IDS) if (scopes.includes(id)) known.push(id)
  for (const s of scopes) if (!SCOPE_IDS.includes(s as (typeof SCOPE_IDS)[number])) known.push(s)
  return known
}

const ScopeChecks: React.FC<{ scopes: ScopeInfo[]; value: string[]; onChange: (v: string[]) => void }> = ({ scopes, value, onChange }) => (
  <fieldset className="rounded border border-gray-200 p-2 dark:border-gray-700">
    <legend className="px-1 text-xs text-gray-500">{t('roles.scopesLegend')}</legend>
    <div className="flex flex-col gap-1">
      {(scopes.length ? scopes.map((s) => s.id) : [...SCOPE_IDS]).map((id) => (
        <label key={id} className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            aria-label={scopeText(scopes.find((x) => x.id === id), id)}
            checked={value.includes(id)}
            onChange={(e) => onChange(e.target.checked ? [...new Set([...value, id])] : value.filter((s) => s !== id))}
          />
          <span>
            <span className="font-medium">{scopeText(scopes.find((x) => x.id === id), id)}</span>
            <span className="ml-1 font-mono text-xs text-gray-500">{id}</span>
          </span>
        </label>
      ))}
    </div>
  </fieldset>
)
