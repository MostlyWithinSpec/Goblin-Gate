// The /log route (task 026): Logs (the event viewer) and the chart builder.
// The view is in the URL (?view=charts) so links can open either.
import { useLocation, useNavigate } from 'react-router-dom'
import LogViewer from './log/LogViewer'
import ChartBuilder from './log/ChartBuilder'

export default function Log() {
  const location = useLocation()
  const navigate = useNavigate()
  const charts = new URLSearchParams(location.search).get('view') === 'charts'
  const tab = (active: boolean) =>
    `rounded px-3 py-1.5 text-sm ${active ? 'bg-blue-600 text-white' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'}`
  return (
    <div className="p-6">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">Log &amp; Report</h1>
        <div className="flex gap-1">
          <button type="button" className={tab(!charts)} onClick={() => navigate('/log')}>Logs</button>
          <button type="button" className={tab(charts)} onClick={() => navigate('/log?view=charts')}>Chart builder</button>
        </div>
      </div>
      {charts ? <ChartBuilder /> : <LogViewer />}
    </div>
  )
}
