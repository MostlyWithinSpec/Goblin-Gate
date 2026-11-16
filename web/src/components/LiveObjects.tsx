// Live address objects (tasks 139/141): an address of type "live" holds no
// addresses itself — a plugin keeps them up to date, and the members change
// without a commit. This module owns everything the admin sees about them: the
// poller (useLiveObjects), the plugin a live address may belong to
// (pluginsNeedingObjects), the row's two lines (LiveObjectCell) and the
// read-only drawer of members (LiveMembersDrawer).
//
// The poller keeps its own state (like GeoIPStatusCard does) so the page works
// with or without a QueryClientProvider.
import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { getLiveObjects, type Address, type Candidate, type LiveObject } from '../api/objects'
import type { PluginStatus } from '../api/plugins'
import { inputClass } from './ConfigList'
import { EditDrawer } from './EditDrawer'
import { t } from '../i18n'

export const LIVE_REFRESH_MS = 10000

export function useLiveObjects(): Map<string, LiveObject> {
  const [objects, setObjects] = useState<LiveObject[]>([])
  useEffect(() => {
    let alive = true
    const load = () =>
      getLiveObjects()
        .then((o) => alive && setObjects(o))
        .catch(() => {})
    load()
    const timer = setInterval(load, LIVE_REFRESH_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [])
  const map = new Map<string, LiveObject>()
  for (const o of objects) map.set(o.name, o)
  return map
}

// pluginsNeedingObjects: the plugins a live address may belong to — the ones
// that ask for objects:write (approved or not), from the installed list and
// from the candidate's enabled plugins. One that asked but was not approved
// stays on the list, marked, so the admin can see what to enable.
export function pluginsNeedingObjects(cfg: Candidate, installed: PluginStatus[]): PluginStatus[] {
  const byName = new Map<string, PluginStatus>()
  for (const p of installed) byName.set(p.name, p)
  for (const p of cfg.plugins ?? []) {
    const cur = byName.get(p.name)
    const caps = [...new Set([...(p.capabilities ?? []), ...(cur?.capabilities ?? [])])]
    byName.set(p.name, { ...(cur ?? {}), name: p.name, capabilities: caps, requested_capabilities: cur?.requested_capabilities ?? caps } as PluginStatus)
  }
  const asks = (p: PluginStatus) =>
    (p.requested_capabilities ?? []).includes('objects:write') || (p.capabilities ?? []).includes('objects:write')
  return [...byName.values()].filter(asks).sort((a, b) => a.name.localeCompare(b.name))
}

// pickLiveObject: the live status of an address row. The API only describes the
// running config, so an address a commit has not published yet has no entry;
// show it as an object its plugin has not filled.
export function pickLiveObject(o: LiveObject | undefined, name: string, plugin: string): LiveObject {
  return o ?? { name, plugin, members: [] }
}

const fmtTime = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

// liveLine: the row's two lines — "N members, kept up to date by <plugin>" and
// the time of the plugin's last update, or that the object is still empty and
// was never updated.
export function liveLine(o: LiveObject): { summary: string; updated: string } {
  const has = !!o.members && o.members.length > 0
  const summary = has
    ? t('liveObjects.count', { count: o.members!.length }) + ', ' + t('liveObjects.by', { plugin: o.plugin })
    : t('liveObjects.empty', { plugin: o.plugin })
  const updated = o.updated ? t('liveObjects.lastUpdate') + ' ' + fmtTime(o.updated) : t('liveObjects.never')
  return { summary, updated }
}

export function LiveObjectCell({ address, objects }: { address: Address; objects: Map<string, LiveObject> }) {
  const [open, setOpen] = useState(false)
  const obj = pickLiveObject(objects.get(address.name), address.name, address.value)
  const line = liveLine(obj)
  return (
    <div>
      <button type="button" className="text-left text-blue-600 hover:underline" onClick={() => setOpen(true)}>
        {line.summary}
      </button>
      <div className="text-xs text-gray-500">{line.updated}</div>
      {open && <LiveMembersDrawer obj={obj} onClose={() => setOpen(false)} />}
    </div>
  )
}

// LiveObjectsHelp: what the form and the drawer say about a live object's
// members, so the same sentence is worded identically in both.
export function LiveObjectsHelp({ className }: { className?: string }) {
  return <p className={className ?? 'text-xs text-gray-500'}>{t('liveObjects.help')}</p>
}

// optionNeedsObjectsWrite: the plugin option text of the address form — a
// plugin that asked for objects:write but was not approved says so, because
// saving a live address on it is refused until the admin enables it.
export function optionNeedsObjectsWrite(name: string, approved: boolean): string {
  return approved ? name : `${name} — ${t('addresses.needsObjectsWrite')}`
}

// LivePluginField: the address form's "Plugin" select for a live object — the
// plugins that may keep it, and the sentence saying why they change on their
// own. A plugin the admin has not approved objects:write for is still listed,
// marked, so it is obvious what to enable.
export function LivePluginField({
  value,
  cfg,
  installed,
  onChange,
  err,
}: {
  value: string
  cfg: Candidate
  installed: PluginStatus[]
  onChange: (v: string) => void
  err: (field: string) => ReactNode
}) {
  return (
    <label className="block">
      {t('addresses.plugin')}
      <select aria-label={t('addresses.plugin')} className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{t('plugins.selectOption')}</option>
        {pluginsNeedingObjects(cfg, installed).map((p) => (
          <option key={p.name} value={p.name}>
            {optionNeedsObjectsWrite(p.name, (p.capabilities ?? []).includes('objects:write'))}
          </option>
        ))}
      </select>
      <LiveObjectsHelp className="mt-1 text-xs text-gray-500" />
      {err('value')}
    </label>
  )
}

export function LiveMembersDrawer({ obj, onClose }: { obj: LiveObject; onClose: () => void }) {
  const line = liveLine(obj)
  return (
    <EditDrawer isOpen onClose={onClose} title={obj.name}>
      <h3 className="mb-2 text-sm font-semibold text-gray-800 dark:text-gray-200">{t('liveObjects.members')}</h3>
      <p className="mb-3 text-sm text-gray-500">{line.updated}</p>
      {!obj.members || obj.members.length === 0 ? (
        <p className="text-sm text-gray-500">{t('liveObjects.empty', { plugin: obj.plugin })}</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {obj.members.map((m) => (
            <li key={m}>
              <code className="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-700">{m}</code>
            </li>
          ))}
        </ul>
      )}
      <LiveObjectsHelp className="mt-4 text-xs text-gray-500" />
      <div className="mt-6 flex justify-end">
        <button type="button" onClick={onClose} className="min-h-[40px] rounded border border-gray-300 px-4 py-1 text-sm">
          {t('common.close')}
        </button>
      </div>
    </EditDrawer>
  )
}
