import React from 'react'
import { NavLink } from 'react-router-dom'

const items = [
  { to: '/plugins/sentry', label: 'Queue', end: true },
  { to: '/plugins/sentry/incidents', label: 'Incidents' },
  { to: '/plugins/sentry/rules', label: 'Rules' },
  { to: '/plugins/sentry/blocks', label: 'Blocks' },
  { to: '/plugins/sentry/learning', label: 'Learning' }
]

export default function SentryNav() {
  return (
    <div className="mb-4 flex gap-1 rounded-lg bg-gray-100 p-1 dark:bg-gray-700">
      {items.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          end={it.end}
          className={({ isActive }) =>
            `rounded px-3 py-1 text-sm transition-colors ${
              isActive
                ? 'bg-white font-medium text-gray-900 shadow-sm dark:bg-gray-800 dark:text-white'
                : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'
            }`
          }
        >
          {it.label}
        </NavLink>
      ))}
    </div>
  )
}
