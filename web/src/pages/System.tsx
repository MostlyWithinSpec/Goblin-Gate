import React from 'react'
import { Link } from 'react-router-dom'

const System: React.FC = () => {
  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-gray-800 dark:text-white mb-6">System Configuration</h1>
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6">
        <div className="space-y-4">
          <Link to="/system/revisions" className="block p-4 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors">
            <h2 className="text-lg font-semibold text-blue-700 dark:text-blue-300">Revisions</h2>
            <p className="text-gray-600 dark:text-gray-300">View and manage configuration history</p>
          </Link>
          <div className="p-4 bg-gray-50 dark:bg-gray-700/30 rounded-lg">
            <h2 className="text-lg font-semibold text-gray-800 dark:text-white mb-2">Coming soon...</h2>
            <p className="text-gray-600 dark:text-gray-300">Additional system configuration options will be available here.</p>
          </div>
        </div>
      </div>
    </div>
  )
}

export default System