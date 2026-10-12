import React, { useState, useEffect } from 'react'
import { Link, useLocation, Outlet } from 'react-router-dom'
import {
  Home,
  Network,
  Shield,
  Settings,
  User,
  ChevronLeft,
  ChevronRight,
  Activity,
  Menu,
  X,
  Lock,
  Rss,
  Globe,
  Filter,
  AppWindow,
  KeyRound,
  Waypoints,
  ShieldAlert
} from 'lucide-react'
import { CommitBar } from './CommitBar'
import { toggleTheme } from '../theme'
import UserMenu from './UserMenu'

const Layout: React.FC = () => {
  const [isSidebarOpen, setIsSidebarOpen] = useState(true)
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false)
  const location = useLocation()

  // Toggle sidebar on mobile
  const toggleSidebar = () => {
    setIsSidebarOpen(!isSidebarOpen)
  }

  // Close mobile menu when navigating
  useEffect(() => {
    setIsMobileMenuOpen(false)
  }, [location])


  // Navigation items
  const navItems = [
    {
      name: 'Dashboard',
      path: '/',
      icon: Home,
      group: 'Dashboard'
    },
    {
      name: 'Network',
      path: '/network',
      icon: Network,
      group: 'Network'
    },
    {
      name: 'Policy & Objects',
      path: '/policy',
      icon: Shield,
      group: 'Policy & Objects'
    },
    {
      name: 'Security Profiles',
      path: '/security',
      icon: Lock,
      group: 'Security Profiles'
    },
    { name: 'Threat Feeds', path: '/security/feeds', icon: Rss, group: 'Security Profiles' },
    { name: 'DNS Filter', path: '/security/dns-filter', icon: Filter, group: 'Security Profiles' },
    { name: 'Web Filter', path: '/security/web-filter', icon: Globe, group: 'Security Profiles' },
    { name: 'Application Control', path: '/security/apps', icon: AppWindow, group: 'Security Profiles' },
    { name: 'IPS', path: '/security/ips', icon: ShieldAlert, group: 'Security Profiles' },
    { name: 'Tailscale', path: '/vpn/tailscale', icon: Waypoints, group: 'VPN' },
    { name: 'WireGuard', path: '/vpn/wireguard', icon: KeyRound, group: 'VPN' },
    {
      name: 'Log & Report',
      path: '/log',
      icon: Activity,
      group: 'Log & Report'
    },
    {
      name: 'System',
      path: '/system',
      icon: Settings,
      group: 'System'
    }
  ]

  // Group navigation items
  const groups = Array.from(
    new Map(navItems.map(item => [item.group, []])).keys()
  )

  return (
    <div className="flex h-screen bg-gray-50 dark:bg-transparent">
      {/* Mobile menu button */}
      <div className="md:hidden fixed top-4 left-4 z-50">
        <button
          onClick={() => setIsMobileMenuOpen(!isMobileMenuOpen)}
          className="p-2 rounded-md bg-white dark:bg-gray-800 shadow-lg"
        >
          {isMobileMenuOpen ? <X size={20} /> : <Menu size={20} />}
        </button>
      </div>

      {/* Sidebar */}
      <div
        className={`fixed md:static inset-y-0 left-0 z-40 bg-white dark:bg-gray-900/80 dark:backdrop-blur border-r border-gray-200 dark:border-gray-700 shadow-lg md:shadow-none transform transition-transform duration-300 ease-in-out ${
          isMobileMenuOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'
        } w-64 flex flex-col`}
      >
        {/* Sidebar header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-200 dark:border-gray-700">
          <div className="flex items-center space-x-2">
            <img src="/goblin-128.png" alt="" className="w-10 h-10 goblin-glow" />
            <span className="font-display text-base font-extrabold tracking-wide text-gray-800 dark:text-gray-100">
              Goblin <span className="text-green-600 dark:text-green-400">Gate</span>
            </span>
          </div>
          <button
            onClick={toggleSidebar}
            className="md:hidden text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
          >
            {isSidebarOpen ? <ChevronLeft size={20} /> : <ChevronRight size={20} />}
          </button>
        </div>

        {/* Sidebar content */}
        <div className="flex-1 overflow-y-auto py-4">
          <nav className="px-2 space-y-1">
            {Array.from(groups).map(group => (
              <div key={group} className="mb-4">
                <div className="px-4 py-2 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                  {group}
                </div>
                {navItems
                  .filter(item => item.group === group)
                  .map(item => {
                    const Icon = item.icon
                    const isActive = location.pathname === item.path
                    return (
                      <Link
                        key={item.path}
                        to={item.path}
                        className={`flex items-center px-4 py-3 text-sm font-medium rounded-lg transition-colors ${
                          isActive
                            ? 'bg-blue-100 text-blue-700 dark:bg-gray-800 dark:text-blue-400'
                            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100'
                        }`}
                      >
                        <Icon size={18} className="mr-3" />
                        <span>{item.name}</span>
                      </Link>
                    )
                  })}
              </div>
            ))}
          </nav>
        </div>

        {/* Sidebar footer */}
        <div className="p-4 border-t border-gray-200 dark:border-gray-700">
          <div className="flex items-center justify-between">
            <div className="flex items-center space-x-3">
              <div className="bg-gray-200 dark:bg-gray-600 rounded-full p-1">
                <User size={20} className="text-gray-700 dark:text-gray-300" />
              </div>
              <div>
                <p className="text-sm font-medium text-gray-800 dark:text-gray-200">Admin</p>
                <p className="text-xs text-gray-500 dark:text-gray-400">Online</p>
              </div>
            </div>
            <button
              onClick={toggleTheme}
              className="p-2 rounded-full bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-300 dark:hover:bg-gray-600"
            >
              {document.documentElement.classList.contains('dark') ? '☀️' : '🌙'}
            </button>
          </div>
        </div>
      </div>

      {/* Overlay for mobile */}
      {isMobileMenuOpen && (
        <div
          className="fixed inset-0 z-30 bg-black bg-opacity-50 md:hidden"
          onClick={() => setIsMobileMenuOpen(false)}
        ></div>
      )}

      {/* Main content */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top bar */}
        <header className="bg-white dark:bg-gray-900/80 dark:backdrop-blur border-b border-gray-200 dark:border-gray-700 shadow-sm z-10">
          <div className="flex items-center justify-between px-4 py-3">
            <div className="flex items-center">
              <button
                onClick={toggleSidebar}
                className="md:hidden mr-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
              >
                <Menu size={20} />
              </button>
              <h1 className="text-lg font-semibold text-gray-800 dark:text-white">Goblin Gate</h1>
            </div>
            <div className="flex items-center space-x-4">
              <div className="relative">
                <div className="bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 rounded-full px-3 py-1 text-sm font-medium">
                  <span className="inline-flex items-center">
                    <div className="w-2 h-2 bg-green-500 rounded-full mr-2"></div>
                    <span>Online</span>
                  </span>
                </div>
              </div>
              <CommitBar />
              <button className="p-2 rounded-full bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-300 dark:hover:bg-gray-600">
                <Activity size={20} />
              </button>
              <UserMenu />
            </div>
          </div>
        </header>

        {/* Page content */}
        <main className="flex-1 overflow-y-auto p-4 md:p-6 bg-gray-50 dark:bg-transparent">
          <div className="max-w-7xl mx-auto">
            <div className="bg-white dark:bg-gray-800/90 border border-gray-200 dark:border-gray-700 rounded-xl shadow-sm p-6">
              <Outlet />
            </div>
          </div>
        </main>
      </div>
    </div>
  )
}

export default Layout