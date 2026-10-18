import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, Check, FileCode2, GitCompare, Search } from 'lucide-react'
import { inputClass } from '../../components/ConfigList'
import {
  getGeneratedFiles,
  getGeneratedFile,
  getGeneratedDiff,
  type GeneratedConfig as ConfigKind,
  type GeneratedFile,
  type GeneratedFileContent,
} from '../../api/system'

// Group files by renderer for the left-hand list.
const groupByRenderer = (files: GeneratedFile[]): Map<string, GeneratedFile[]> => {
  const groups = new Map<string, GeneratedFile[]>()
  for (const f of files) {
    const list = groups.get(f.renderer) ?? []
    list.push(f)
    groups.set(f.renderer, list)
  }
  return groups
}

// A single line of a unified diff, coloured by its prefix.
const DiffLine: React.FC<{ line: string }> = ({ line }) => {
  if (line.startsWith('---') || line.startsWith('+++')) {
    return <div className="px-2 font-mono text-xs text-gray-500">{line}</div>
  }
  if (line.startsWith('@@')) {
    return <div className="bg-blue-50 px-2 font-mono text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-300">{line}</div>
  }
  if (line.startsWith('+')) {
    return (
      <div className="bg-green-50 px-2 font-mono text-xs text-green-700 dark:bg-green-900/20 dark:text-green-400">
        {line}
      </div>
    )
  }
  if (line.startsWith('-')) {
    return (
      <div className="bg-red-50 px-2 font-mono text-xs text-red-600 line-through dark:bg-red-900/20">
        {line}
      </div>
    )
  }
  return <div className="px-2 font-mono text-xs text-gray-700 dark:text-gray-300">{line}</div>
}

const GeneratedConfig: React.FC = () => {
  const [config, setConfig] = useState<ConfigKind>('running')
  const [files, setFiles] = useState<GeneratedFile[]>([])
  const [listError, setListError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [content, setContent] = useState<GeneratedFileContent | null>(null)
  const [contentError, setContentError] = useState<string | null>(null)
  const [diff, setDiff] = useState<string | null>(null)
  const [showDiff, setShowDiff] = useState(false)
  const [copied, setCopied] = useState(false)

  // Load the file list whenever the running/candidate toggle changes.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setListError(null)
    setSelected(null)
    setContent(null)
    setDiff(null)
    setShowDiff(false)
    getGeneratedFiles(config === 'candidate' ? 'candidate' : undefined)
      .then((fs) => {
        if (cancelled) return
        setFiles(fs)
      })
      .catch((e: Error) => {
        if (!cancelled) setListError(e.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [config])

  // Load the selected file's content.
  useEffect(() => {
    if (!selected) return
    let cancelled = false
    setContentError(null)
    setDiff(null)
    setShowDiff(false)
    getGeneratedFile(selected, config === 'candidate' ? 'candidate' : undefined)
      .then((c) => {
        if (!cancelled) setContent(c)
      })
      .catch((e: Error) => {
        if (!cancelled) setContentError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [selected, config])

  // Load the candidate diff on demand.
  const loadDiff = useCallback(async (path: string) => {
    setShowDiff(true)
    setContentError(null)
    try {
      const r = await getGeneratedDiff(path)
      setDiff(r.diff)
    } catch (e) {
      setDiff(null)
      setContentError((e as Error).message)
    }
  }, [])

  const copyContent = async () => {
    if (!content) return
    try {
      await navigator.clipboard.writeText(content.content)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard unavailable (e.g. insecure context); ignore.
    }
  }

  const groups = useMemo(() => {
    const q = search.trim().toLowerCase()
    const filtered = q ? files.filter((f) => f.path.toLowerCase().includes(q)) : files
    return groupByRenderer(filtered)
  }, [files, search])

  return (
    <div className="space-y-4">
      {/* Toolbar: running/candidate toggle + search */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="inline-flex rounded-lg border border-gray-300 dark:border-gray-600" role="group" aria-label="Config source">
          {(['running', 'candidate'] as const).map((c) => (
            <button
              key={c}
              type="button"
              onClick={() => setConfig(c)}
              className={`px-3 py-1.5 text-sm first:rounded-l-lg last:rounded-r-lg ${
                config === c
                  ? 'bg-blue-600 text-white'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700'
              }`}
            >
              {c}
            </button>
          ))}
        </div>
        <label className="relative flex-1 min-w-[200px] max-w-sm">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            type="search"
            aria-label="Filter files"
            placeholder="Filter files…"
            className={`${inputClass} pl-8`}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
      </div>

      {listError && (
        <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">
          {listError}
        </div>
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(260px,1fr)_3fr]">
        {/* File list grouped by renderer */}
        <div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800/50 p-2 max-h-[70vh] overflow-y-auto">
          {loading ? (
            <p className="p-2 text-sm text-gray-500">Loading…</p>
          ) : groups.size === 0 ? (
            <p className="p-2 text-sm text-gray-500">{search ? 'No matching files.' : 'No generated files.'}</p>
          ) : (
            [...groups.entries()].map(([renderer, list]) => (
              <div key={renderer} className="mb-3 last:mb-0">
                <h3 className="px-2 py-1 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {renderer}
                </h3>
                <ul>
                  {list.map((f) => (
                    <li key={f.path}>
                      <button
                        type="button"
                        onClick={() => setSelected(f.path)}
                        className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm ${
                          selected === f.path
                            ? 'bg-blue-50 font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
                            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700'
                        }`}
                      >
                        <FileCode2 size={14} className="shrink-0 text-gray-400" />
                        <span className="truncate">{f.path}</span>
                        {f.redacted && (
                          <span className="ml-auto shrink-0 rounded bg-yellow-100 px-1.5 text-[10px] font-medium text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-300">
                            redacted
                          </span>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ))
          )}
        </div>

        {/* Content / diff pane */}
        <div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800/50">
          {!selected ? (
            <p className="p-4 text-sm text-gray-500">Select a file to view its generated content.</p>
          ) : (
            <div className="flex h-full flex-col">
              <div className="flex items-center gap-2 border-b border-gray-200 p-2 dark:border-gray-700">
                <span className="truncate font-mono text-sm text-gray-700 dark:text-gray-200">{selected}</span>
                <div className="ml-auto flex shrink-0 items-center gap-2">
                  {config === 'candidate' && (
                    <button
                      type="button"
                      onClick={() => loadDiff(selected)}
                      className={`inline-flex items-center gap-1 rounded border px-2.5 py-1 text-xs ${
                        showDiff
                          ? 'border-blue-600 bg-blue-600 text-white'
                          : 'border-gray-300 text-gray-700 hover:bg-gray-100 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-700'
                      }`}
                    >
                      <GitCompare size={12} /> {showDiff ? 'Hide diff' : 'Diff vs running'}
                    </button>
                  )}
                  {content && !showDiff && (
                    <button
                      type="button"
                      onClick={copyContent}
                      className="inline-flex items-center gap-1 rounded border border-gray-300 px-2.5 py-1 text-xs text-gray-700 hover:bg-gray-100 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-700"
                    >
                      {copied ? <Check size={12} /> : <Copy size={12} />} {copied ? 'Copied' : 'Copy'}
                    </button>
                  )}
                </div>
              </div>

              <div className="max-h-[60vh] overflow-auto">
                {contentError && (
                  <div className="m-2 rounded bg-red-50 p-2 text-sm text-red-700" role="alert">
                    {contentError}
                  </div>
                )}

                {showDiff ? (
                  diff === null ? (
                    <p className="p-3 text-sm text-gray-500">Loading diff…</p>
                  ) : diff === '' ? (
                    <p className="p-3 text-sm text-gray-500" data-testid="diff-unchanged">
                      No differences from running config.
                    </p>
                  ) : (
                    <div data-testid="unified-diff" className="py-1">
                      {diff.split('\n').map((line, i) => (
                        <DiffLine key={i} line={line} />
                      ))}
                    </div>
                  )
                ) : content ? (
                  <>
                    {(content.redacted || content.truncated) && (
                      <div className="m-2 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-300">
                        {content.redacted && 'Secrets are redacted in this view. '}
                        {content.truncated && 'File is truncated (over 2 MB).'}
                      </div>
                    )}
                    <pre className="p-2 font-mono text-xs leading-5 text-gray-800 dark:text-gray-100">
                      {content.content.split('\n').map((line, i) => (
                        <div key={i} className="flex">
                          <span className="w-10 shrink-0 select-none pr-3 text-right text-gray-400">{i + 1}</span>
                          <span className="whitespace-pre-wrap break-all">{line || ' '}</span>
                        </div>
                      ))}
                    </pre>
                  </>
                ) : (
                  !contentError && <p className="p-3 text-sm text-gray-500">Loading…</p>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

export default GeneratedConfig
