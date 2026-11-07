// Network → Devices: live device inventory from the firewall.
import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Search, Trash2, ExternalLink, Plus, ShieldAlert, Power } from 'lucide-react'
import { listDevices, renameDevice, forgetDevice, wakeDevice, type DeviceInventory } from '../../api/devices'
import { createBlock } from '../../api/sentry'
import { APIRequestError } from '../../api/client'
import { EditDrawer } from '../../components/EditDrawer'
import { Dialog } from '../../components/Dialog'
import { Badge } from '../../components/Badge'
import { t } from '../../i18n'

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'

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

export default function Devices() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const now = useNow(30000)

  const [search, setSearch] = useState('')
  const [interfaceFilter, setInterfaceFilter] = useState('')
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

  const q = useQuery({
    queryKey: ['devices', interfaceFilter],
    queryFn: () => listDevices(interfaceFilter || undefined),
    retry: false,
    refetchInterval: 30000,
  })

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

  const devices = q.data ?? []
  const interfaces = Array.from(new Set(devices.map((d) => d.interface).filter(Boolean))) as string[]

  const filtered = devices.filter((d) => {
    if (interfaceFilter && d.interface !== interfaceFilter) return false
    if (!search) return true
    const s = search.toLowerCase()
    return (
      (d.name ?? '').toLowerCase().includes(s) ||
      (d.hostname ?? '').toLowerCase().includes(s) ||
      (d.vendor ?? '').toLowerCase().includes(s) ||
      d.ips.some((ip) => ip.includes(s)) ||
      d.mac.toLowerCase().includes(s)
    )
  })

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
      </div>

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
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.name')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.vendor')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.ips')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.mac')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.interface')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.firstSeen')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('devices.col.lastSeen')}</th>
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
                <tr><td colSpan={7} className="px-3 py-6 text-center text-gray-500">{t('devices.none')}</td></tr>
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
            </div>

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
