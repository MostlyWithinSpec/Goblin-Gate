import { NavLink, Outlet } from 'react-router-dom'
import { t } from '../i18n'

// The /network section: a tab bar over the sub-pages (the active one highlighted).
const tabs: [string, string][] = [
  ['interfaces', t('network.interfaces')],
  ['addresses', t('network.addresses')],
  ['address-groups', t('network.addrGroups')],
  ['services', t('network.services')],
  ['service-groups', t('network.serviceGroups')],
  ['port-forwards', t('network.portForwards')],
  ['upnp', t('network.upnp')],
  ['reverse-proxy', t('network.reverseProxy')],
  ['dhcp', t('network.dhcp')],
  ['dns', t('network.dns')],
  ['gateways', t('network.gateways')],
  ['gateway-groups', t('network.gatewayGroups')],
  ['static-routes', t('network.staticRoutes')],
  ['devices', t('network.devices')],
  ['dynamic-dns', t('network.dynamicDns')],
]

export default function Network() {
  return (
    <div className="p-6">
      <h1 className="mb-6 text-2xl font-bold text-gray-800 dark:text-gray-100">{t('network.configuration')}</h1>
      <nav className="mb-6 flex flex-wrap gap-2 border-b border-gray-200 pb-3 dark:border-gray-700">
        {tabs.map(([path, label]) => (
          <NavLink
            key={path}
            to={`/network/${path}`}
            className={({ isActive }) =>
              `rounded-lg px-3 py-1.5 text-sm font-bold ${
                isActive
                  ? 'bg-blue-600 text-white'
                  : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-100'
              }`
            }
          >
            {label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  )
}
