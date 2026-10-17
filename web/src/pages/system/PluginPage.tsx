// A plugin's own page (manifest ui.pages), shown in a sandboxed iframe:
// goblind serves it from /plugin-ui/<name>/<path> with a CSP sandbox, and
// the iframe sandbox (no allow-same-origin) keeps the page's scripts away
// from the admin session and the API.
import React from 'react'
import { Link, useParams } from 'react-router-dom'

export default function PluginPage() {
  const { name = '', '*': path = '' } = useParams()
  const valid = /^[a-z][a-z0-9-]{0,31}$/.test(name) && !path.split('/').includes('..')
  return (
    <div className="p-6">
      <div className="mb-3 flex items-center justify-between">
        <h1 className="text-xl font-semibold text-gray-800 dark:text-gray-100">
          Plugin {name}: {path}
        </h1>
        <Link to="/system/plugins" className="text-sm text-blue-600 hover:underline dark:text-blue-300">
          All plugins
        </Link>
      </div>
      {valid ? (
        <iframe
          title={`Plugin ${name} page ${path}`}
          src={`/plugin-ui/${name}/${path}`}
          sandbox="allow-scripts allow-forms"
          referrerPolicy="no-referrer"
          className="h-[70vh] w-full rounded border border-gray-200 bg-white dark:border-gray-700"
        />
      ) : (
        <p role="alert" className="text-sm text-red-600">Not a plugin page.</p>
      )}
    </div>
  )
}
