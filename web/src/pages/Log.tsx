// The /log route (task 026): Logs (the event viewer), the chart builder and
// packet capture (task 077, its own page at /log/capture).
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { t } from '../i18n'
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
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">{t('log.title')}</h1>
        <div className="flex flex-wrap gap-1">
          <button type="button" className={tab(!charts)} onClick={() => navigate('/log')}>{t('log.tab.logs')}</button>
          <button type="button" className={tab(charts)} onClick={() => navigate('/log?view=charts')}>{t('log.tab.charts')}</button>
          <Link to="/log/capture" className={tab(false)}>{t('log.tab.capture')}</Link>
          <Link to="/log/live" className={tab(false)}>{t('log.tab.live')}</Link>
          <Link to="/log/export" className={tab(false)}>{t('log.tab.export')}</Link>
        </div>
      </div>
      {charts ? <ChartBuilder /> : <LogViewer />}
    </div>
  )
}
