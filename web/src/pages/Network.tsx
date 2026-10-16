import { NavLink, Outlet } from 'react-router-dom'

// The /network section: a tab bar over the sub-pages (the active one highlighted).
const tabs = [
  ['interfaces', 'Interfaces'],
  ['addresses', 'Addresses'],
  ['address-groups', 'Address Groups'],
  ['services', 'Services'],
  ['service-groups', 'Service Groups'],
  ['port-forwards', 'Port Forwards'],
  ['upnp', 'UPnP'],
  ['dhcp', 'DHCP'],
  ['dns', 'DNS'],
  ['gateways', 'Gateways'],
  ['gateway-groups', 'Gateway Groups'],
  ['static-routes', 'Static Routes'],
] as const

export default function Network() {
  return (
    <div className="p-6">
      <h1 className="mb-6 text-2xl font-bold text-gray-800 dark:text-gray-100">Network Configuration</h1>
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
