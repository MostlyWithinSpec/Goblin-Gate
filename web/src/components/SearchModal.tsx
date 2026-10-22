// SearchModal: the Ctrl+K command palette. It searches the menu pages and
// the candidate's named objects (utils/search.ts); with an empty query it
// shows the recent picks. Arrows move, Enter opens, Esc closes.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Search } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getCandidate } from '../api/objects'
import { buildSearchIndex, search, type SearchItem } from '../utils/search'

const RECENT_KEY = 'goblin-search-recent'

function loadRecent(): SearchItem[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
    return Array.isArray(v) ? v.slice(0, 8) : []
  } catch {
    return []
  }
}

function saveRecent(items: SearchItem[]) {
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(items.slice(0, 8)))
  } catch {
    // storage unavailable: recent picks are a convenience
  }
}

interface Props {
  isOpen: boolean
  onClose: () => void
}

export const SearchModal: React.FC<Props> = ({ isOpen, onClose }) => {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(0)
  const [recent, setRecent] = useState<SearchItem[]>(loadRecent)
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)
  const candidate = useQuery({ queryKey: ['candidate'], queryFn: getCandidate, enabled: isOpen })

  const index = useMemo(() => buildSearchIndex(candidate.data ?? {}), [candidate.data])
  const results = useMemo(() => (query.trim() ? search(index, query) : recent), [index, query, recent])

  useEffect(() => {
    if (isOpen) {
      setQuery('')
      setSelected(0)
      inputRef.current?.focus()
    }
  }, [isOpen])

  if (!isOpen) return null

  const pick = (item: SearchItem) => {
    const next = [item, ...recent.filter((r) => r.id !== item.id)]
    setRecent(next.slice(0, 8))
    saveRecent(next)
    onClose()
    navigate(item.path)
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      onClose()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelected((s) => Math.min(s + 1, results.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelected((s) => Math.max(s - 1, 0))
    } else if (e.key === 'Enter' && results[selected]) {
      e.preventDefault()
      pick(results[selected])
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 px-4 pt-[10vh]" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-label="Search"
        className="w-full max-w-xl overflow-hidden rounded-lg bg-white shadow-xl dark:bg-gray-800"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-gray-200 px-3 dark:border-gray-700">
          <Search size={18} className="text-gray-400" />
          <input
            ref={inputRef}
            aria-label="Search pages and objects"
            className="w-full bg-transparent py-3 text-sm outline-none dark:text-white"
            placeholder="Search pages, policies, addresses, devices…"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setSelected(0)
            }}
            onKeyDown={onKey}
          />
          <kbd className="hidden rounded bg-gray-100 px-1.5 text-xs text-gray-500 sm:inline dark:bg-gray-700">Esc</kbd>
        </div>
        <ul role="listbox" aria-label="Results" className="max-h-80 overflow-y-auto py-1">
          {!query.trim() && results.length > 0 && <li className="px-3 py-1 text-xs uppercase text-gray-400">Recent</li>}
          {results.map((item, i) => (
            <li key={item.id} role="option" aria-selected={i === selected}>
              <button
                type="button"
                onMouseEnter={() => setSelected(i)}
                onClick={() => pick(item)}
                className={`flex w-full items-center gap-3 px-3 py-2 text-left text-sm ${i === selected ? 'bg-blue-50 dark:bg-blue-900/40' : ''}`}
              >
                <span className="w-24 shrink-0 text-xs text-gray-500 dark:text-gray-400">{item.type}</span>
                <span className="truncate font-medium text-gray-800 dark:text-gray-100">{item.name}</span>
                {item.detail && item.type !== 'Page' && <span className="ml-auto truncate text-xs text-gray-400">{item.detail}</span>}
              </button>
            </li>
          ))}
          {query.trim() && results.length === 0 && <li className="px-3 py-6 text-center text-sm text-gray-500">No matches.</li>}
          {!query.trim() && results.length === 0 && <li className="px-3 py-6 text-center text-sm text-gray-500">Type to search pages and objects.</li>}
        </ul>
      </div>
    </div>
  )
}
