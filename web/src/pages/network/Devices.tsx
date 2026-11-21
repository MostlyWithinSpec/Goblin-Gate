// Network → Devices: live device inventory from the firewall.
import React, { useEffect, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { ArrowDown, ArrowUp, ArrowRightLeft, ChevronDown, ExternalLink, Plus, Power, Search, ShieldAlert, Trash2 } from 'lucide-react'
import {
  listDevices,
  renameDevice,
  forgetDevice,
  wakeDevice,
  type DeviceInventory,
} from '../../api/devices'
import { createBlock } from '../../api/sentry'
import { getCandidate } from '../../api/objects'
import { getRadiusSessions, setDeviceVLAN } from '../../api/radius'
import { usePermissions } from '../../api/permissions'
import { APIRequestError } from '../../api/client'
import { EditDrawer } from '../../components/EditDrawer'
import { Dialog } from '../../components/Dialog'
import { Badge } from '../../components/Badge'
import { t } from '../../i18n'

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'

// knownFieldLabel titles the fields plugins commonly report; "" for others.
function knownFieldLabel(field: string): string {
  switch (field) {
    case 'ap':
      return t('devices.field.ap')
    case 'ssid':
      return t('devices.field.ssid')
    case 'vlan':
      return t('devices.field.vlan')
    case 'link_mbps':
      return t('devices.field.linkMbps')
    case 'connection':
      return t('devices.field.connection')
    case 'band':
      return t('devices.field.band')
    case 'network':
      return t('devices.field.network')
    case 'switch':
      return t('devices.field.switch')
    case 'port':
      return t('devices.field.port')
  }
  return ''
}

// deviceLabel names a device the way the table does.
function deviceLabel(d: { name?: string; hostname?: string; mac: string }): string {
  return d.name || d.hostname || d.mac
}

function timeAgo(iso: string, now: number): string {
  const diff = Math.max(0, (now - new Date(iso).getTime()) / 1000)
  if (diff < 60) return t('devices.secondsAgo', { count: Math.floor(diff) })
  if (diff < 3600) return t('devices.minutesAgo', { count: Math.floor(diff / 60) })
  if (diff < 86400) return t('devices.hoursAgo', { count: Math.floor(diff / 3600) })
  return t('devices.daysAgo', { count: Math.floor(diff / 86400) })
}

function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  React.useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

// blockErrorMessage words the API's answer to a block request. The server's own
// message is data, so it goes in as a placeholder of a whole sentence.
function blockErrorMessage(e: unknown): string {
  if (e instanceof APIRequestError && e.code === 'protected_target')
    return t('devices.blockProtected', { message: (e as Error).message })
  return (e as Error).message
}

// An annotation column is what a plugin reports about where a device sits, shown
// as its own column. Fields are free-form per plugin, so the set of
// columns comes from the data, never from a fixed list.
type AnnotationColumn = {
  key: string
  label: string
  // fields are the annotation fields this column reads and plugin is the one it
  // reads them from, so every cell of a column reads alike.
  fields: string[]
  plugin: string
}

// A field a fixed column already titles, so it should not get a column of its own.
const fixedFields = new Set(['mac', 'ips', 'hostname', 'interface', 'vendor', 'name'])

// Column titles. A plugin's fields are free-form, so a field shows
// under its own name: port → "Port", link_mbps → "Link mbps", ap → "Ap".
function annotationLabel(field: string): string {
  const known = knownFieldLabel(field)
  if (known) return known
  const spaced = field.replace(/_/g, ' ')
  return spaced.charAt(0).toLocaleUpperCase() + spaced.slice(1)
}

// A switch and its port join up into one column: "Switch A · port 5" reads better
// than two columns that are usually empty on their own.
const switchPortFields = ['switch', 'port']
const switchPortKey = 'switch port'

// annotationColumns collects the columns the devices in front of us deserve, in
// the order their fields turn up. A device can be annotated by several plugins
// and no plugin annotates every device, so a field qualifies as soon as any one
// device carries it.
function annotationColumns(devices: DeviceInventory[]): AnnotationColumn[] {
  const out: AnnotationColumn[] = []
  const seen = new Set(fixedFields)
  // has answers whether a plugin has all of want on at least one device here.
  const has = (plugin: string, want: string[]) =>
    devices.some((d) => want.every((f) => f in (d.annotations?.[plugin] ?? {})))
  for (const d of devices) {
    for (const [plugin, fields] of Object.entries(d.annotations ?? {})) {
      const names = Object.keys(fields)
      if (has(plugin, switchPortFields) && !seen.has(switchPortKey)) {
        seen.add(switchPortKey)
        seen.add('switch')
        seen.add('port')
        out.push({ key: switchPortKey, label: t('devices.col.switchPort'), fields: switchPortFields, plugin })
      }
      for (const field of names) {
        if (seen.has(field)) continue
        seen.add(field)
        out.push({ key: field, label: annotationLabel(field), fields: [field], plugin })
      }
    }
  }
  return out
}

// cellText is a column's cell: a switch and its port join up, anything else
// stands on its own.
function cellText(d: DeviceInventory, col: AnnotationColumn): string {
  const fields = d.annotations?.[col.plugin] ?? {}
  const values = col.fields.map((f) => fields[f] ?? '')
  if (!values[0]) return ''
  return col.fields.length > 1 ? t('devices.switchPortCell', { switch: values[0], port: values[1] }) : values[0]
}

const COLS_KEY = 'goblin.deviceColumns'

// loadShownCols is which annotation columns this browser has switched on. None
// by default: they are only interesting with a plugin that reports them.
function loadShownCols(): string[] {
  try {
    const raw = localStorage.getItem(COLS_KEY)
    const v: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(v) ? v.filter((k): k is string => typeof k === 'string') : []
  } catch {
    /* storage blocked or junk: the defaults stand */
  }
  return []
}

function saveShownCols(keys: string[]): void {
  try {
    localStorage.setItem(COLS_KEY, JSON.stringify(keys))
  } catch {
    /* storage blocked: the choice lasts until reload */
  }
}

// annotationText is every annotation value of a device, for the search box.
function annotationText(d: DeviceInventory): string {
  const parts: string[] = []
  for (const fields of Object.values(d.annotations ?? {})) parts.push(...Object.values(fields))
  return parts.join(' ').toLowerCase()
}

// pluginsOf lists the plugins that annotated anything here, then those that only
// reported an update time, so the drawer covers every plugin the gate knows.
function pluginsOf(devices: DeviceInventory[], updated: Record<string, string> | undefined): string[] {
  const out = new Set<string>()
  for (const d of devices) for (const p of Object.keys(d.annotations ?? {})) out.add(p)
  for (const p of Object.keys(updated ?? {})) out.add(p)
  return [...out].sort()
}

type SortKey = 'name' | 'vendor' | 'ips' | 'mac' | 'interface' | 'vlan' | 'first_seen' | 'last_seen' | string

const defaultSort: { key: SortKey; dir: 1 | -1 } = { key: 'last_seen', dir: -1 }

// sortDevices orders the table. A column's cells all come from one plugin, so
// sorting an annotation column compares like with like.
function sortDevices(rows: DeviceInventory[], key: SortKey, dir: 1 | -1, cols: AnnotationColumn[]): DeviceInventory[] {
  const annCol = cols.find((c) => c.key === key)
  const value = (d: DeviceInventory): string => {
    if (annCol) return cellText(d, annCol).toLowerCase()
    switch (key) {
      case 'name':
        return (d.name || d.hostname || d.mac).toLowerCase()
      case 'vendor':
        return (d.vendor ?? '').toLowerCase()
      case 'ips':
        return d.ips[0] ?? ''
      case 'interface':
        return d.interface ?? ''
      case 'vlan':
        return d.vlan ?? ''
      case 'first_seen':
        return d.first_seen
      default:
        return d.last_seen
    }
  }
  return [...rows].sort((a, b) => dir * value(a).localeCompare(value(b)))
}

// SortHeader is a column title you can sort by: the first click sorts A→Z, the
// next one flips it.
function SortHeader({ label, col, sort, onSort }: {
  label: string
  col: SortKey
  sort: { key: SortKey; dir: 1 | -1 }
  onSort: (key: SortKey) => void
}) {
  const active = sort.key === col
  return (
    <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">
      <button type="button" onClick={() => onSort(col)} className="flex items-center gap-1 hover:text-gray-700 dark:hover:text-gray-300">
        {label}
        {active && (sort.dir === 1 ? <ArrowUp size={12} /> : <ArrowDown size={12} />)}
      </button>
    </th>
  )
}


export default function Devices() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const now = useNow(30000)

  const [search, setSearch] = useState('')
  const [interfaceFilter, setInterfaceFilter] = useState('')
  const [shownCols, setShownCols] = useState<string[]>(loadShownCols)
  const [colsOpen, setColsOpen] = useState(false)
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>(defaultSort)
  const [selected, setSelected] = useState<DeviceInventory | null>(null)
  const [name, setName] = useState('')
  const [blockOpen, setBlockOpen] = useState(false)
  const [blockIp, setBlockIp] = useState('')
  const [blockMinutes, setBlockMinutes] = useState(60)
  const [blockReason, setBlockReason] = useState('')
  const [blockAllowInternal, setBlockAllowInternal] = useState(false)
  const [blockError, setBlockError] = useState<string | null>(null)
  // the last try was refused because the IP is on one of the box's networks:
  // the dialog then offers to block it anyway
  const [blockProtected, setBlockProtected] = useState(false)
  // Move to VLAN: the device, the VLAN picked, and what happened
  const perms = usePermissions()
  const canMove = perms.can('write:network')
  const [moving, setMoving] = useState<DeviceInventory | null>(null)
  const [moveTo, setMoveTo] = useState('')
  const [moveMsg, setMoveMsg] = useState<string | null>(null)

  const q = useQuery({
    queryKey: ['devices', interfaceFilter],
    queryFn: () => listDevices(interfaceFilter || undefined),
    retry: false,
    refetchInterval: 30000,
  })

  // the VLANs and the quarantine VLAN (RADIUS), and who is in quarantine now
  const cfgQ = useQuery({ queryKey: ['candidate'], queryFn: getCandidate, retry: false })
  const vlans = (cfgQ.data?.vlans ?? []).map((v) => v.name)
  const radiusOn = !!cfgQ.data?.radius?.enabled
  const quarantineVlan = cfgQ.data?.radius?.unknown === 'quarantine' ? cfgQ.data?.radius?.quarantine_vlan ?? '' : ''
  const sessQ = useQuery({
    queryKey: ['radius-sessions'],
    queryFn: getRadiusSessions,
    retry: false,
    enabled: radiusOn,
    refetchInterval: 30000,
  })
  const quarantined = new Set(
    (sessQ.data?.sessions ?? []).filter((x) => quarantineVlan && x.vlan === quarantineVlan).map((x) => x.mac),
  )
  const inQuarantine = (d: DeviceInventory) => !d.vlan && quarantined.has(d.mac)

  const moveMut = useMutation({
    mutationFn: ({ mac, vlan }: { mac: string; vlan: string }) => setDeviceVLAN(mac, vlan),
    onSuccess: (res, { vlan }) => {
      const label = deviceLabel(res.device)
      const to = vlan || t('devices.vlanNone')
      const r = res.disconnect?.result
      setMoveMsg(r && r !== 'acked' ? t('devices.movedNoAck', { device: label, to, result: r }) : t('devices.moved', { device: label, to }))
      setMoving(null)
      setSelected((cur) => (cur && cur.mac === res.device.mac ? { ...cur, vlan: res.device.vlan } : cur))
      qc.invalidateQueries({ queryKey: ['devices'] })
      qc.invalidateQueries({ queryKey: ['radius-sessions'] })
    },
  })
  const openMove = (d: DeviceInventory) => {
    moveMut.reset()
    setMoveTo(d.vlan && vlans.includes(d.vlan) ? d.vlan : vlans.find((v) => v !== quarantineVlan) ?? '')
    setMoving(d)
  }

  const renameMut = useMutation({
    mutationFn: (mac: string) => renameDevice(mac, name.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      setSelected(null)
    },
  })

  const forgetMut = useMutation({
    mutationFn: (mac: string) => forgetDevice(mac),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      setSelected(null)
    },
  })

  const [wakeMsg, setWakeMsg] = useState<string | null>(null)
  const wakeMut = useMutation({
    mutationFn: (mac: string) => wakeDevice(mac),
    onSuccess: (_data, mac) => {
      const d = devices.find((x) => x.mac === mac)
      setWakeMsg(t('devices.wakeSent', { name: d?.name || d?.hostname || mac }))
    },
  })

  const blockMut = useMutation({
    mutationFn: () => createBlock({ ip: blockIp, minutes: blockMinutes, reason: blockReason, allow_internal: blockAllowInternal }),
    onSuccess: () => {
      setBlockOpen(false)
      setBlockError(null)
      setBlockProtected(false)
      setBlockReason('')
      setBlockAllowInternal(false)
    },
    onError: (e) => {
      const isProtected = e instanceof APIRequestError && e.code === 'protected_target'
      setBlockProtected(isProtected)
      setBlockError(blockErrorMessage(e))
    },
  })

  const devices = q.data?.devices ?? []
  const annotationsUpdated = q.data?.annotations_updated
  const interfaces = Array.from(new Set(devices.map((d) => d.interface).filter(Boolean))) as string[]
  const annCols = annotationColumns(devices)
  const shown = annCols.filter((c) => shownCols.includes(c.key))

  const searched = devices.filter((d) => {
    if (interfaceFilter && d.interface !== interfaceFilter) return false
    if (!search) return true
    const s = search.toLowerCase()
    const inFixed =
      (d.name ?? '').toLowerCase().includes(s) ||
      (d.hostname ?? '').toLowerCase().includes(s) ||
      (d.vendor ?? '').toLowerCase().includes(s) ||
      d.ips.some((ip) => ip.includes(s)) ||
      d.mac.toLowerCase().includes(s)
    return inFixed || annotationText(d).includes(s)
  })
  const filtered = sortDevices(searched, sort.key, sort.dir, annCols)

  // pickCol switches one annotation column on or off and remembers it here.
  const pickCol = (key: string, on: boolean) => {
    const next = on ? [...new Set([...shownCols, key])] : shownCols.filter((k) => k !== key)
    setShownCols(next)
    saveShownCols(next)
  }

  // sortBy flips the direction on the same column, else starts on a new one.
  const sortBy = (key: SortKey) => {
    setSort(sort.key === key ? { key, dir: sort.dir === 1 ? -1 : 1 } : { key, dir: 1 })
  }

  // The columns menu closes when a press lands outside it, like the account menu
  // does. The button is part of that wrapper, so pressing it never counts as
  // outside: the button itself decides, and one press delivers both a mousedown
  // and a click without the menu closing in between.
  const colsRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!colsOpen) return
    const onDown = (e: MouseEvent) => {
      if (colsRef.current && !colsRef.current.contains(e.target as Node)) setColsOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [colsOpen])

  const openDevice = (d: DeviceInventory) => {
    setSelected(d)
    setName(d.name ?? '')
    setWakeMsg(null)
  }

  const submitBlock = () => {
    if (!blockIp || !blockReason) {
      setBlockError(t('devices.blockRequired'))
      return
    }
    blockMut.mutate()
  }

  // 503 devices_unavailable
  if (q.isError && (q.error as any)?.code === 'devices_unavailable') {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-gray-700 dark:bg-gray-800">
        <p className="text-sm text-gray-500">{t('devices.unavailable')}</p>
      </div>
    )
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">{t('devices.title')}</h2>
      </div>

      {/* Filters */}
      <div className="mb-4 flex flex-wrap gap-3">
        <div className="relative">
          <Search size={16} className="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            type="text"
            placeholder={t('devices.searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full sm:w-64 rounded border border-gray-300 py-1 pl-8 pr-2 text-sm dark:bg-gray-700 dark:border-gray-600"
          />
        </div>
        <select
          value={interfaceFilter}
          onChange={(e) => setInterfaceFilter(e.target.value)}
          className="rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600"
        >
          <option value="">{t('devices.allInterfaces')}</option>
          {interfaces.map((i) => (
            <option key={i} value={i}>{i}</option>
          ))}
        </select>
        {annCols.length > 0 && (
          <div className="relative" ref={colsRef}>
            <button
              type="button"
              onClick={() => setColsOpen((v) => !v)}
              aria-haspopup="menu"
              aria-expanded={colsOpen}
              className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600"
            >
              {t('devices.showColumns')}
              <ChevronDown size={14} />
            </button>
            {colsOpen && (
              <div
                role="menu"
                aria-label={t('devices.showColumns')}
                className="absolute z-10 mt-1 w-56 rounded-lg border border-gray-200 bg-white p-2 shadow-lg dark:border-gray-700 dark:bg-gray-800"
              >
                {annCols.map((c) => (
                  <label key={c.key} className="flex items-center gap-2 px-1 py-1 text-sm">
                    <input
                      type="checkbox"
                      checked={shownCols.includes(c.key)}
                      onChange={(e) => pickCol(c.key, e.target.checked)}
                    />
                    <span>{c.label}</span>
                    <span className="text-xs text-gray-400">{t('devices.columnSource', { plugin: c.plugin })}</span>
                  </label>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {moveMsg && (
        <div role="status" className="mb-3 rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/30 dark:text-green-200">{moveMsg}</div>
      )}

      {/* Table */}
      {q.isLoading && <p className="text-sm text-gray-500">{t('common.loading')}</p>}
      {q.isError && (q.error as any)?.code !== 'devices_unavailable' && (
        <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700">{(q.error as Error).message}</div>
      )}

      {!q.isLoading && !q.isError && (
        <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
          <table className="min-w-full divide-y divide-gray-200 bg-white text-sm dark:divide-gray-700 dark:bg-gray-800 dark:text-gray-200">
            <thead className="bg-gray-50 dark:bg-gray-700">
              <tr>
                <SortHeader label={t('devices.col.name')} col="name" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.vendor')} col="vendor" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.ips')} col="ips" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.mac')} col="mac" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.interface')} col="interface" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.vlan')} col="vlan" sort={sort} onSort={sortBy} />
                {shown.map((c) => (
                  <SortHeader key={c.key} label={c.label} col={c.key} sort={sort} onSort={sortBy} />
                ))}
                <SortHeader label={t('devices.col.firstSeen')} col="first_seen" sort={sort} onSort={sortBy} />
                <SortHeader label={t('devices.col.lastSeen')} col="last_seen" sort={sort} onSort={sortBy} />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
              {filtered.map((d) => (
                <tr
                  key={d.mac}
                  onClick={() => openDevice(d)}
                  className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700/40"
                >
                  <td className="px-3 py-2 font-medium">
                    {d.name || d.hostname || '—'}
                    {d.randomized && (
                      <span className="ml-2"><Badge variant="warning">{t('devices.randomized')}</Badge></span>
                    )}
                    {inQuarantine(d) && (
                      <span className="ml-2"><Badge variant="error">{t('devices.quarantined')}</Badge></span>
                    )}
                  </td>
                  <td className="px-3 py-2">{d.vendor ?? '—'}</td>
                  <td className="px-3 py-2 font-mono text-xs">
                    {d.ips[0]}
                    {d.ips.length > 1 && (
                      <span title={d.ips.slice(1).join('\n')} className="ml-1 cursor-help text-gray-400">
                        +{d.ips.length - 1}
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 font-mono text-xs">{d.mac}</td>
                  <td className="px-3 py-2">{d.interface ?? '—'}</td>
                  <td className="px-3 py-2">
                    {d.vlan || '—'}
                    {canMove && inQuarantine(d) && (
                      <button
                        type="button"
                        onClick={(e) => { e.stopPropagation(); openMove(d) }}
                        className="ml-2 text-xs text-blue-600 hover:underline"
                        aria-label={t('devices.approveDevice', { device: deviceLabel(d) })}
                      >
                        {t('devices.approve')}
                      </button>
                    )}
                  </td>
                  {shown.map((c) => (
                    <td key={c.key} className="px-3 py-2">{cellText(d, c) || '—'}</td>
                  ))}
                  <td className="px-3 py-2 text-gray-500">{timeAgo(d.first_seen, now)}</td>
                  <td className="px-3 py-2">
                    {timeAgo(d.last_seen, now)}
                    {now - new Date(d.last_seen).getTime() < 5 * 60 * 1000 && (
                      <span className="ml-2"><Badge variant="success">{t('devices.online')}</Badge></span>
                    )}
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr><td colSpan={8 + shown.length} className="px-3 py-6 text-center text-gray-500">{t('devices.none')}</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* Drawer */}
      {selected && (
        <EditDrawer isOpen onClose={() => setSelected(null)} title={selected.name || selected.hostname || selected.mac}>
          <div className="space-y-4 text-sm">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-gray-600 dark:text-gray-300">
              <div><span className="text-gray-400">{t('devices.field.mac')}</span> <code>{selected.mac}</code></div>
              <div><span className="text-gray-400">{t('devices.field.interface')}</span> {selected.interface ?? '—'}</div>
              <div><span className="text-gray-400">{t('devices.field.vendor')}</span> {selected.vendor ?? '—'}</div>
              <div><span className="text-gray-400">{t('devices.field.hostname')}</span> {selected.hostname ?? '—'}</div>
              <div className="sm:col-span-2"><span className="text-gray-400">{t('devices.field.ips')}</span> {selected.ips.join(', ')}</div>
              <div><span className="text-gray-400">{t('devices.field.firstSeen')}</span> {new Date(selected.first_seen).toLocaleString()}</div>
              <div><span className="text-gray-400">{t('devices.field.lastSeen')}</span> {timeAgo(selected.last_seen, now)}</div>
              <div>
                <span className="text-gray-400">{t('devices.vlanLabel')}</span> {selected.vlan || t('devices.vlanNone')}
                {inQuarantine(selected) && (
                  <span className="ml-2"><Badge variant="error">{t('devices.quarantined')}</Badge></span>
                )}
              </div>
            </div>
            {canMove && vlans.length > 0 && (
              <button
                type="button"
                onClick={() => openMove(selected)}
                className="flex items-center gap-1 text-sm text-blue-600 hover:underline"
              >
                <ArrowRightLeft size={14} /> {inQuarantine(selected) ? t('devices.approve') : t('devices.moveVlan')}
              </button>
            )}

            {/* What plugins report about where this device sits (read-only). */}
            {pluginsOf([selected], annotationsUpdated).map((plugin) => (
              <div key={plugin} className="border-t border-gray-200 pt-4 dark:border-gray-600">
                <p className="text-xs font-semibold uppercase text-gray-400">
                  {t('devices.fromPlugin', { plugin })}
                </p>
                <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2 text-gray-600 dark:text-gray-300">
                  {Object.entries(selected.annotations?.[plugin] ?? {})
                    .sort(([a], [b]) => a.localeCompare(b))
                    .map(([field, value]) => (
                      <div key={field}>
                        <span className="text-gray-400">{t('devices.fromField', { label: annotationLabel(field) })}</span>
                        {value}
                      </div>
                    ))}
                </div>
                {annotationsUpdated?.[plugin] && (
                  <p className="mt-2 text-xs text-gray-400">
                    {t('devices.fromUpdated', { when: timeAgo(annotationsUpdated[plugin], now) })}
                  </p>
                )}
              </div>
            ))}

            {/* Rename */}
            <form
              onSubmit={(e) => { e.preventDefault(); renameMut.mutate(selected.mac) }}
              className="space-y-2"
            >
              <label className="block">
                {t('devices.nameLabel')}
                <input
                  className={inputClass}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t('devices.namePlaceholder')}
                  maxLength={63}
                />
              </label>
              {renameMut.isError && (
                <p role="alert" className="text-xs text-red-600">{(renameMut.error as Error).message}</p>
              )}
              <button
                type="submit"
                disabled={renameMut.isPending}
                className="rounded bg-blue-600 px-3 py-1 text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {renameMut.isPending ? t('devices.saving') : t('devices.saveName')}
              </button>
            </form>

            {/* Shortcuts (they need an address) */}
            {selected.ips.length > 0 && <div className="space-y-2 border-t border-gray-200 pt-4 dark:border-gray-600">
              <p className="text-xs font-semibold uppercase text-gray-400">{t('devices.shortcuts')}</p>
              <button
                type="button"
                onClick={() => navigate(`/log?src_ip=${encodeURIComponent(selected.ips[0])}`)}
                className="flex items-center gap-1 text-sm text-blue-600 hover:underline"
              >
                <ExternalLink size={14} /> {t('devices.showLogs')}
              </button>
              <button
                type="button"
                onClick={() => navigate(`/network/addresses?new_host=${encodeURIComponent(selected.ips[0])}`)}
                className="flex items-center gap-1 text-sm text-blue-600 hover:underline"
              >
                <Plus size={14} /> {t('devices.createAddress')}
              </button>
              <button
                type="button"
                onClick={() => {
                  setBlockIp(selected.ips[0])
                  setBlockError(null)
                  setBlockProtected(false)
                  setBlockAllowInternal(false)
                  setBlockOpen(true)
                }}
                className="flex items-center gap-1 text-sm text-red-600 hover:underline"
              >
                <ShieldAlert size={14} /> {t('devices.blockTemp')}
              </button>
              <button
                type="button"
                title={t('devices.wakeHint')}
                onClick={() => wakeMut.mutate(selected.mac)}
                disabled={wakeMut.isPending}
                className="flex items-center gap-1 text-sm text-blue-600 hover:underline disabled:opacity-50"
              >
                <Power size={14} /> {wakeMut.isPending ? t('devices.waking') : t('devices.wake')}
              </button>
              {wakeMsg && (
                <p className="text-xs text-green-600 dark:text-green-400">{wakeMsg}</p>
              )}
              {wakeMut.isError && (
                <p role="alert" className="text-xs text-red-600">{(wakeMut.error as Error).message}</p>
              )}
            </div>}

            {/* Forget */}
            <div className="border-t border-gray-200 pt-4 dark:border-gray-600">
              <button
                type="button"
                onClick={() => {
                  if (window.confirm(t('devices.forgetConfirm', { mac: selected.mac }))) {
                    forgetMut.mutate(selected.mac)
                  }
                }}
                disabled={forgetMut.isPending}
                className="flex items-center gap-1 rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50"
              >
                <Trash2 size={14} /> {forgetMut.isPending ? t('devices.forgetting') : t('devices.forget')}
              </button>
              {forgetMut.isError && (
                <p role="alert" className="mt-1 text-xs text-red-600">{(forgetMut.error as Error).message}</p>
              )}
            </div>
          </div>
        </EditDrawer>
      )}

      {/* Move to VLAN dialog */}
      <Dialog open={!!moving} onClose={() => setMoving(null)} title={t('devices.moveTitle')} size="small">
        {moving && (
          <div className="space-y-4 text-sm">
            <label className="block">
              {t('devices.moveTo')}
              <select value={moveTo} onChange={(e) => setMoveTo(e.target.value)} className={inputClass}>
                <option value="">{t('devices.vlanNone')}</option>
                {vlans.map((v) => (
                  <option key={v} value={v}>{v}</option>
                ))}
              </select>
            </label>
            <p>
              {t('devices.moveConfirm', {
                device: deviceLabel(moving),
                from: moving.vlan || (inQuarantine(moving) ? quarantineVlan : t('devices.vlanNone')),
                to: moveTo || t('devices.vlanNone'),
              })}
            </p>
            {moveMut.isError && (
              <p role="alert" className="text-xs text-red-600">{(moveMut.error as Error).message}</p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setMoving(null)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">{t('common.cancel')}</button>
              <button
                type="button"
                disabled={moveMut.isPending || moveTo === (moving.vlan ?? '')}
                onClick={() => moveMut.mutate({ mac: moving.mac, vlan: moveTo })}
                className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {moveMut.isPending ? t('devices.moving') : t('devices.move')}
              </button>
            </div>
          </div>
        )}
      </Dialog>

      {/* Block dialog */}
      <Dialog open={blockOpen} onClose={() => setBlockOpen(false)} title={t('devices.blockTitle')} size="small">
        <div className="space-y-4">
          {blockError && (
            <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-300">
              {blockError}
            </div>
          )}
          <label className="block text-sm">
            {t('devices.block.ip')}
            <input type="text" value={blockIp} onChange={(e) => setBlockIp(e.target.value)} className={inputClass} />
          </label>
          <label className="block text-sm">
            {t('devices.block.duration')}
            <input type="number" min={1} max={10080} value={blockMinutes} onChange={(e) => setBlockMinutes(Number(e.target.value))} className={inputClass} />
          </label>
          <label className="block text-sm">
            {t('devices.block.reason')}
            <textarea value={blockReason} onChange={(e) => setBlockReason(e.target.value)} rows={2} maxLength={500} className={inputClass} />
          </label>
          {blockProtected && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={blockAllowInternal} onChange={(e) => setBlockAllowInternal(e.target.checked)} />
              {t('devices.block.allowInternal')}
            </label>
          )}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setBlockOpen(false)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">{t('common.cancel')}</button>
            <button
              type="button"
              disabled={blockMut.isPending}
              onClick={submitBlock}
              className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50"
            >
              {blockMut.isPending ? t('devices.block.blocking') : t('devices.block.block')}
            </button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
