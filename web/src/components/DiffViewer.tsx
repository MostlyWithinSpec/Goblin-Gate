import React from 'react'

interface DiffChange {
  path: string
  old?: any
  new?: any
}

interface DiffViewerProps {
  changes: DiffChange[]
}

export const DiffViewer: React.FC<DiffViewerProps> = ({ changes }) => {
  // Group changes by section (top-level keys)
  const groupedChanges: Record<string, DiffChange[]> = {}
  
  changes.forEach(change => {
    const section = change.path.split('.')[0]
    if (!groupedChanges[section]) {
      groupedChanges[section] = []
    }
    groupedChanges[section].push(change)
  })
  
  const formatValue = (value: any): string => {
    if (value === null || value === undefined) {
      return 'null'
    }
    if (typeof value === 'object') {
      return JSON.stringify(value, null, 2)
    }
    return String(value)
  }
  
  return (
    <div className="space-y-4">
      {Object.entries(groupedChanges).map(([section, sectionChanges]) => (
        <div key={section} className="border rounded-lg overflow-hidden">
          <div className="bg-gray-100 dark:bg-gray-800 px-4 py-2 font-medium">
            {section.charAt(0).toUpperCase() + section.slice(1)}
          </div>
          <div className="divide-y divide-gray-200 dark:divide-gray-700">
            {sectionChanges.map((change, index) => (
              <div key={index} className="px-4 py-3">
                <div className="font-mono text-sm mb-2">
                  <span className="text-gray-500">{change.path}</span>
                </div>
                <div className="flex space-x-4">
                  <div className="flex-1">
                    <div className="text-xs text-gray-500 mb-1">Old</div>
                    <div className="bg-red-50 dark:bg-red-900/20 p-2 rounded font-mono text-sm break-all">
                      {change.old !== undefined ? formatValue(change.old) : '—'}
                    </div>
                  </div>
                  <div className="flex-1">
                    <div className="text-xs text-gray-500 mb-1">New</div>
                    <div className="bg-green-50 dark:bg-green-900/20 p-2 rounded font-mono text-sm break-all">
                      {change.new !== undefined ? formatValue(change.new) : '—'}
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}