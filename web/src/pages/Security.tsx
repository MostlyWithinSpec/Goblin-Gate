// The /security landing page: one card per security profile page.
import { Link } from 'react-router-dom'
import { t } from '../i18n'

const cards = [
  { to: '/security/feeds', title: t('security.feedsTitle'), text: t('security.feedsText') },
  { to: '/security/dns-filter', title: t('security.dnsFilterTitle'), text: t('security.dnsFilterText') },
  { to: '/security/web-filter', title: t('security.webFilterTitle'), text: t('security.webFilterText') },
  { to: '/security/ips', title: t('security.ipsTitle'), text: t('security.ipsText') },
  { to: '/security/apps', title: t('security.appsTitle'), text: t('security.appsText') },
  { to: '/security/crowdsec', title: t('security.crowdSecTitle'), text: t('security.crowdSecText') },
]

export default function Security() {
  return (
    <div className="p-6">
      <h1 className="mb-6 text-2xl font-bold text-gray-800 dark:text-white">{t('security.title')}</h1>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        {cards.map((c) => (
          <Link key={c.to} to={c.to} className="block rounded-lg bg-white p-6 shadow-sm hover:shadow dark:bg-gray-800 min-h-[40px]">
            <h2 className="mb-2 text-lg font-semibold text-gray-800 dark:text-white">{c.title}</h2>
            <p className="text-gray-600 dark:text-gray-300">{c.text}</p>
          </Link>
        ))}
      </div>
      <p className="mt-6 text-sm text-gray-500">{t('security.footer')}</p>
    </div>
  )
}
