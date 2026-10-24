import React, { useEffect, useRef, useState } from 'react'
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { Clock, FlaskConical, GripVertical, MoreVertical, Plus } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import {
  getCandidate,
  getPolicyHits,
  objectsOf,
  savePolicies,
  type Policy,
  type PolicyObjects,
  type SimProtocol,
  type SimulateRequest,
  type ValidationError,
} from '../../api/policy'
import PolicyEditor from './PolicyEditor'
import PolicySimulator from './PolicySimulator'
import { clean, clonePolicy, groupByPair, insertAt, move, newPolicy, profileBadges, replace } from './policyLogic'

const Chips = ({ items }: { items?: string[] }) => (
  <div className="flex flex-wrap gap-1">
    {(items ?? []).map((i) => (
      <span key={i} className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-gray-700 dark:text-gray-200">
        {i}
      </span>
    ))}
  </div>
)

interface RowProps {
  p: Policy
  hits: Record<number, number> | null
  draggable: boolean
  menuOpen: boolean
  onEdit: () => void
  onToggle: () => void
  onMenu: (open: boolean) => void
  onAction: (a: MenuAction) => void
}

type MenuAction = 'clone' | 'above' | 'below' | 'toggle' | 'delete'

// menuPosition places the row menu under its button, right-aligned, or
// above it when there is no room below.
const MENU_HEIGHT = 140
function menuPosition(button: HTMLElement | null): React.CSSProperties {
  if (!button) return {}
  const r = button.getBoundingClientRect()
  const right = Math.max(window.innerWidth - r.right, 8)
  return r.bottom + MENU_HEIGHT > window.innerHeight ? { bottom: window.innerHeight - r.top + 4, right } : { top: r.bottom + 4, right }
}

function Row({ p, hits, draggable, menuOpen, onEdit, onToggle, onMenu, onAction }: RowProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: p.id, disabled: !draggable })
  const style: React.CSSProperties = {
    transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
    transition,
    opacity: isDragging ? 0.5 : undefined,
  }
  const menuButton = useRef<HTMLButtonElement>(null)
  const hit = hits && p.id in hits ? hits[p.id].toLocaleString() : '—'
  return (
    <tr
      ref={setNodeRef}
      style={style}
      data-testid={`policy-row-${p.id}`}
      className={`border-b border-gray-100 dark:border-gray-700 ${p.enabled ? '' : 'opacity-50'}`}
      onContextMenu={(e) => {
        e.preventDefault()
        onMenu(true)
      }}
    >
      <td className="px-1">
        {draggable && (
          <button type="button" aria-label={`Drag policy ${p.id}`} className="cursor-grab text-gray-400" {...attributes} {...listeners}>
            <GripVertical size={16} />
          </button>
        )}
      </td>
      <td className="px-2 py-1 text-sm">{p.id}</td>
      <td className="px-2 py-1 text-sm">
        <button type="button" className="text-blue-600 hover:underline dark:text-blue-400" onClick={onEdit}>
          {p.name}
        </button>
      </td>
      <td className="px-2 py-1">
        <div className="flex items-center gap-1">
          <Chips items={p.src_intf} />
          <span className="text-gray-400">→</span>
          <Chips items={p.dst_intf} />
        </div>
      </td>
      <td className="px-2 py-1"><Chips items={p.src_addr} /></td>
      <td className="px-2 py-1"><Chips items={p.dst_addr} /></td>
      <td className="px-2 py-1"><Chips items={p.service} /></td>
      <td className="px-2 py-1 text-sm">
        <span className={`rounded px-1.5 py-0.5 text-xs font-medium text-white ${p.action === 'accept' ? 'bg-green-600' : 'bg-red-600'}`}>
          {p.action === 'accept' ? 'ACCEPT' : 'DENY'}
        </span>
      </td>
      <td className="px-2 py-1 text-sm">{p.nat ? 'NAT' : ''}</td>
      <td className="px-2 py-1">
        <div className="flex gap-1">
          {profileBadges(p).map((b) => (
            <span key={b} className="rounded bg-purple-100 px-1 text-[10px] font-semibold text-purple-800 dark:bg-purple-900 dark:text-purple-200">
              {b}
            </span>
          ))}
        </div>
      </td>
      <td className="px-2 py-1 text-sm">{p.log && p.log !== 'off' ? p.log : ''}</td>
      <td className="px-2 py-1 text-sm">
        {p.schedule && (
          <span className="inline-flex items-center gap-1 text-xs text-gray-600 dark:text-gray-300">
            <Clock size={12} />
            {p.schedule}
          </span>
        )}
      </td>
      <td className="px-2 py-1 text-right text-sm tabular-nums">{hit}</td>
      <td className="px-2 py-1">
        <input type="checkbox" aria-label={`Enable policy ${p.id}`} checked={p.enabled} onChange={onToggle} />
      </td>
      {/* pinned to the right edge so the actions stay visible on a wide
          table; the menu is fixed-positioned so the scroll box can't clip it */}
      <td className="sticky right-0 bg-white px-1 dark:bg-gray-800">
        <button
          ref={menuButton}
          type="button"
          aria-label={`Actions for policy ${p.id}`}
          onClick={() => onMenu(!menuOpen)}
          className="rounded p-1 text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-700"
        >
          <MoreVertical size={16} />
        </button>
        {menuOpen && (
          <div
            role="menu"
            style={menuPosition(menuButton.current)}
            className="fixed z-20 w-40 rounded border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-700 dark:bg-gray-800"
          >
            {(
              [
                ['clone', 'Clone'],
                ['above', 'Insert above'],
                ['below', 'Insert below'],
                ['toggle', p.enabled ? 'Disable' : 'Enable'],
                ['delete', 'Delete'],
              ] as [MenuAction, string][]
            ).map(([a, label]) => (
              <button
                key={a}
                role="menuitem"
                type="button"
                className={`block w-full px-3 py-1 text-left text-sm hover:bg-gray-100 dark:hover:bg-gray-700 ${a === 'delete' ? 'text-red-600' : 'dark:text-gray-200'}`}
                onClick={() => {
                  onMenu(false)
                  onAction(a)
                }}
              >
                {label}
              </button>
            ))}
          </div>
        )}
      </td>
    </tr>
  )
}

interface Editing {
  policy: Policy
  isNew: boolean
  insertAt?: number // where a new policy goes; undefined = end
}

export default function Policies() {
  const location = useLocation()
  const [policies, setPolicies] = useState<Policy[]>([])
  const [objects, setObjects] = useState<PolicyObjects | null>(null)
  const [hits, setHits] = useState<Record<number, number> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [grouped, setGrouped] = useState(false)
  const [menuFor, setMenuFor] = useState<number | null>(null)
  const [editing, setEditing] = useState<Editing | null>(null)
  // non-null = the simulator panel is open; the value is the prefill (from
  // ?simulate=1… or a log event's "Why was this denied?")
  const [sim, setSim] = useState<SimulateRequest | null>(null)
  const sensors = useSensors(useSensor(PointerSensor), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))

  useEffect(() => {
    let live = true
    getCandidate()
      .then((cfg) => {
        if (!live) return
        setPolicies(cfg.policies ?? [])
        setObjects(objectsOf(cfg))
        // /policy?edit=<id> (the log viewer's "Go to policy") opens that policy
        const edit = Number(new URLSearchParams(location.search).get('edit'))
        const target = (cfg.policies ?? []).find((p) => p.id === edit)
        if (target) setEditing({ policy: target, isNew: false })
        // /policy?simulate=1&src_ip=… opens the simulator with a prefilled flow
        const q = new URLSearchParams(location.search)
        if (q.get('simulate')) {
          const prefill: SimulateRequest = {
            src_ip: q.get('src_ip') ?? '',
            dst_ip: q.get('dst_ip') ?? '',
            protocol: (['tcp', 'udp', 'icmp'] as SimProtocol[]).includes(q.get('protocol') as SimProtocol)
              ? (q.get('protocol') as SimProtocol)
              : 'any',
          }
          const port = Number(q.get('dst_port'))
          if (Number.isInteger(port) && port > 0) prefill.dst_port = port
          const intf = q.get('src_intf')
          if (intf) prefill.src_intf = intf
          const at = q.get('at')
          if (at) prefill.at = at
          const config = q.get('config')
          if (config === 'candidate' || config === 'running') prefill.config = config
          setSim(prefill)
        }
      })
      .catch((e: Error) => live && setError(e.message))
    getPolicyHits().then((h) => live && setHits(h))
    return () => {
      live = false
    }
  }, [location.search])

  // the row menu is fixed-positioned: close it when anything scrolls, on a
  // click outside it (its own button toggles it) and on Escape
  useEffect(() => {
    if (menuFor === null) return
    const close = () => setMenuFor(null)
    const onDown = (e: MouseEvent) => {
      const t = e.target as Element | null
      if (t?.closest?.('[role="menu"]') || t?.closest?.(`[aria-label="Actions for policy ${menuFor}"]`)) return
      close()
    }
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    window.addEventListener('scroll', close, true)
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('scroll', close, true)
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [menuFor])

  // save stores the new list in the candidate; on failure the old list is kept.
  const save = async (next: Policy[]) => {
    const prev = policies
    setPolicies(next)
    setError(null)
    try {
      const res = await savePolicies(next.map(clean))
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('policies')))
    } catch (e) {
      setPolicies(prev)
      setError((e as Error).message)
    }
  }

  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const from = policies.findIndex((p) => p.id === active.id)
    const to = policies.findIndex((p) => p.id === over.id)
    if (from >= 0 && to >= 0) save(move(policies, from, to))
  }

  const onAction = (p: Policy, a: MenuAction) => {
    const i = policies.findIndex((o) => o.id === p.id)
    switch (a) {
      case 'clone':
        return save(insertAt(policies, i + 1, clonePolicy(p, policies)))
      case 'above':
        return setEditing({ policy: newPolicy(policies), isNew: true, insertAt: i })
      case 'below':
        return setEditing({ policy: newPolicy(policies), isNew: true, insertAt: i + 1 })
      case 'toggle':
        return save(replace(policies, p.id, { ...p, enabled: !p.enabled }))
      case 'delete':
        if (window.confirm(`Delete policy ${p.id} "${p.name}"?`)) save(policies.filter((o) => o.id !== p.id))
    }
  }

  const onEditorSave = (p: Policy) => {
    if (!editing) return
    const next = editing.isNew
      ? insertAt(policies, editing.insertAt ?? policies.length, p)
      : replace(policies, editing.policy.id, p)
    setEditing(null)
    save(next)
  }

  const row = (p: Policy) => (
    <Row
      key={p.id}
      p={p}
      hits={hits}
      draggable={!grouped}
      menuOpen={menuFor === p.id}
      onEdit={() => setEditing({ policy: p, isNew: false })}
      onToggle={() => save(replace(policies, p.id, { ...p, enabled: !p.enabled }))}
      onMenu={(open) => setMenuFor(open ? p.id : null)}
      onAction={(a) => onAction(p, a)}
    />
  )

  const head = ['', 'ID', 'Name', 'From → To', 'Source', 'Destination', 'Service', 'Action', 'NAT', 'Security', 'Log', 'Schedule', 'Hits', 'On', '']
  return (
    <div className="p-6">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">Firewall Policy</h1>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-1 text-sm text-gray-700 dark:text-gray-300">
            <input type="checkbox" checked={grouped} onChange={(e) => setGrouped(e.target.checked)} />
            Group by interface pair
          </label>
          <button
            type="button"
            disabled={!objects}
            onClick={() => setSim({ src_ip: '', dst_ip: '', protocol: 'any' })}
            className="flex items-center gap-1 rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-700"
          >
            <FlaskConical size={16} /> Simulate
          </button>
          <button
            type="button"
            disabled={!objects}
            onClick={() => setEditing({ policy: newPolicy(policies), isNew: true })}
            className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
          >
            <Plus size={16} /> New policy
          </button>
        </div>
      </div>

      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900 dark:text-red-200">{error}</div>}
      {serverErrors.length > 0 && (
        <div className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900 dark:text-yellow-100">
          Saved to the candidate, but a commit would be rejected:
          <ul className="ml-4 list-disc">
            {serverErrors.map((v) => (
              <li key={v.path + v.message}>
                <code>{v.path}</code>: {v.message}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="overflow-x-auto rounded-lg bg-white shadow-sm dark:bg-gray-800">
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
          <SortableContext items={policies.map((p) => p.id)} strategy={verticalListSortingStrategy}>
            <table className="min-w-full">
              <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700 dark:text-gray-300">
                <tr>
                  {head.map((h, i) => (
                    <th key={i} className={`px-2 py-2 font-medium ${i === head.length - 1 ? 'sticky right-0 bg-gray-50 dark:bg-gray-700' : ''}`}>{h}</th>
                  ))}
                </tr>
              </thead>
              {grouped ? (
                groupByPair(policies).map((g) => (
                  <tbody key={g.key}>
                    <tr className="bg-gray-100 dark:bg-gray-900">
                      <td colSpan={head.length} className="px-2 py-1 text-sm font-semibold text-gray-700 dark:text-gray-200">
                        {g.key} <span className="font-normal text-gray-500">({g.policies.length})</span>
                      </td>
                    </tr>
                    {g.policies.map(row)}
                  </tbody>
                ))
              ) : (
                <tbody>{policies.map(row)}</tbody>
              )}
            </table>
          </SortableContext>
        </DndContext>
        {objects && policies.length === 0 && <p className="p-6 text-center text-sm text-gray-500">No policies yet: all forwarded traffic is denied.</p>}
      </div>
      {!grouped && policies.length > 1 && (
        <p className="mt-2 text-xs text-gray-500">Policies match top to bottom; drag rows to reorder.</p>
      )}

      {editing && objects && (
        <PolicyEditor
          policy={editing.policy}
          isNew={editing.isNew}
          objects={objects}
          others={policies.filter((o) => editing.isNew || o.id !== editing.policy.id)}
          onSave={onEditorSave}
          onClose={() => setEditing(null)}
        />
      )}

      {sim && objects && (
        <PolicySimulator
          interfaces={objects.interfaces}
          initial={sim}
          onClose={() => setSim(null)}
          onOpenPolicy={(id) => {
            // the editor opens in place of the simulator (both are drawers)
            const target = policies.find((p) => p.id === id)
            if (!target) return
            setSim(null)
            setEditing({ policy: target, isNew: false })
          }}
        />
      )}
    </div>
  )
}
