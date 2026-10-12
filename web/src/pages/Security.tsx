// The /security landing page: one card per security profile page.
import { Link } from 'react-router-dom'

const cards = [
  { to: '/security/feeds', title: 'Threat Feeds', text: 'Download IP, domain and URL block lists and block them on WAN or per policy.' },
  { to: '/security/dns-filter', title: 'DNS Filter', text: 'Block domain feeds and lists in DNS, force safe search.' },
  { to: '/security/web-filter', title: 'Web Filter', text: 'Allow, monitor, warn or block websites by category.' },
  { to: '/security/ips', title: 'Intrusion Prevention', text: 'Inspect traffic with Suricata and the ET Open rules; block attacks or just log them.' },
  { to: '/security/apps', title: 'Application Control', text: 'Allow, monitor or block applications such as streaming, games or VPNs.' },
]

export default function Security() {
  return (
    <div className="p-6">
      <h1 className="mb-6 text-2xl font-bold text-gray-800 dark:text-white">Security Profiles</h1>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        {cards.map((c) => (
          <Link key={c.to} to={c.to} className="block rounded-lg bg-white p-6 shadow-sm hover:shadow dark:bg-gray-800">
            <h2 className="mb-2 text-lg font-semibold text-gray-800 dark:text-white">{c.title}</h2>
            <p className="text-gray-600 dark:text-gray-300">{c.text}</p>
          </Link>
        ))}
      </div>
      <p className="mt-6 text-sm text-gray-500">Profiles take effect when a firewall policy selects them.</p>
    </div>
  )
}
