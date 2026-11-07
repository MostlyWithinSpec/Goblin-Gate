import React from 'react'
import { NavLink } from 'react-router-dom'
import { t } from '../../../i18n'

// Each tab takes its label from navTabName: a constant whose value is a t() call at
// module load would freeze English, and the keys must stay literal for the catalog
// check, so they live in a switch.
const items: { to: string; key: string; end?: boolean }[] = [
  { to: '/plugins/sentry', key: 'queue', end: true },
  { to: '/plugins/sentry/incidents', key: 'incidents' },
  { to: '/plugins/sentry/rules', key: 'rules' },
  { to: '/plugins/sentry/blocks', key: 'blocks' },
  { to: '/plugins/sentry/learning', key: 'learning' }
]

function navTabName(key: string): string {
  switch (key) {
    case 'incidents':
      return t('sentry.navIncidents')
    case 'rules':
      return t('sentry.navRules')
    case 'blocks':
      return t('sentry.navBlocks')
    case 'learning':
      return t('sentry.navLearning')
    default:
      return t('sentry.navQueue')
  }
}

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
          {navTabName(it.key)}
        </NavLink>
      ))}
    </div>
  )
}
