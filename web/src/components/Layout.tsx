import React, { useState, useEffect } from 'react'
import { Link, useLocation, Outlet } from 'react-router-dom'
import {
  Home,
  Network,
  Shield,
  Settings,
  User,
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
  ShieldAlert,
  Clock,
  Terminal,
  Server,
  History,
  Bell,
  Download,
  Puzzle,
  Inbox,
  ScrollText,
  ListOrdered,
  Ban,
  Camera,
  Search
} from 'lucide-react'
import { CommitBar } from './CommitBar'
import { PageErrorBoundary } from './PageErrorBoundary'
import { toggleTheme } from '../theme'
import UserMenu from './UserMenu'
import { getUpdates } from '../api/system'
import { getSystemStatus } from '../api/config'
import { getPlugins } from '../api/plugins'
import { useQuery } from '@tanstack/react-query'
import { SearchModal } from './SearchModal'

// ConnectionBadge shows whether goblind answers (GET /api/v1/system/status
// every 15 s).
const ConnectionBadge: React.FC = () => {
  const q = useQuery({ queryKey: ['system', 'status', 'badge'], queryFn: getSystemStatus, refetchInterval: 15000, retry: false })
  const online = q.isSuccess
  const label = q.isLoading ? 'Connecting' : online ? 'Online' : 'Offline'
  const reboot = q.data?.reboot_required
  const pkgs = q.data?.reboot_packages ?? []
  return (
    <>
    {reboot && (
      <Link
        to="/system"
        className="rounded-full bg-amber-100 px-3 py-1 text-sm font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200"
        title={`A security update is installed and needs a restart to take effect${pkgs.length ? ` (${pkgs.join(', ')})` : ''}. Restart from System when it suits you.`}
      >
        Restart needed
      </Link>
    )}
    <div
      className="bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 rounded-full px-3 py-1 text-sm font-medium"
      title={online ? 'Goblin Gate is answering' : 'Goblin Gate is not answering'}
    >
      <span className="inline-flex items-center">
        <span className={`w-2 h-2 rounded-full mr-2 ${q.isLoading ? 'bg-gray-400' : online ? 'bg-green-500' : 'bg-red-500'}`}></span>
        <span>{label}</span>
      </span>
    </div>
    </>
  )
}

// mdPx is Tailwind's md breakpoint: below it the sidebar is a slide-in drawer
// that a menu button opens, at it and above the sidebar is part of the layout.
// The drawer always starts closed; matchMedia tracks the viewport and the
// innerWidth check is the fallback for hosts without it (tests, older WebViews).
const mdPx = 768
const mdQuery = `(max-width: ${mdPx - 1}px)`

function isPhone(): boolean {
  if (typeof window.matchMedia === 'function') return window.matchMedia(mdQuery).matches
  return window.innerWidth < mdPx
}

const Layout: React.FC = () => {
  // The drawer starts closed on every viewport: on a phone the page is what
  // you see first, and the menu button reveals the navigation.
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false)
  const [phone, setPhone] = useState(isPhone)
  const [updateAvailable, setUpdateAvailable] = useState(false)
  const [isSearchOpen, setIsSearchOpen] = useState(false)
  const location = useLocation()

  // Close the drawer when navigating
  useEffect(() => {
    setIsMobileMenuOpen(false)
  }, [location])

  // Below md the sidebar is a slide-in drawer, so a viewport that shrinks past
  // md is closed and one that grows past md has nothing left to close (there
  // the sidebar is part of the layout).
  useEffect(() => {
    const onResize = () => {
      const p = isPhone()
      setPhone(p)
      if (!p) setIsMobileMenuOpen(false)
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // Escape closes the drawer.
  useEffect(() => {
    if (!isMobileMenuOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setIsMobileMenuOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [isMobileMenuOpen])

  // Handle global keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Ctrl+K or Cmd+K to open search
      if ((e.ctrlKey || e.metaKey) && e.key === 'k' && !isSearchOpen) {
        e.preventDefault()
        setIsSearchOpen(true)
      }
      // Escape to close search
      if (e.key === 'Escape' && isSearchOpen) {
        setIsSearchOpen(false)
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isSearchOpen])

  // Show a small badge on the System item when an update is available.
  useEffect(() => {
    let cancelled = false
    getUpdates(false)
      .then((s) => {
        if (!cancelled && s.available && s.available !== '') setUpdateAvailable(true)
      })
      .catch(() => { /* ignore — badge stays off */ })
    return () => { cancelled = true }
  }, [])

  // Show the Goblin SOC group only when the Sentry plugin is installed.
  const pluginsQ = useQuery({ queryKey: ['plugins'], queryFn: getPlugins, retry: false })
  const sentryInstalled = (pluginsQ.data?.plugins ?? []).some(
    (p) => p.name === 'sentry' && p.state !== 'not_installed'
  )

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
    { name: 'Schedules', path: '/policy/schedules', icon: Clock, group: 'Policy & Objects' },
    { name: 'Custom Rules', path: '/policy/custom-rules', icon: Terminal, group: 'Policy & Objects' },
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
    { name: 'CrowdSec', path: '/security/crowdsec', icon: ShieldAlert, group: 'Security Profiles' },
    { name: 'Tailscale', path: '/vpn/tailscale', icon: Waypoints, group: 'VPN' },
    { name: 'WireGuard', path: '/vpn/wireguard', icon: KeyRound, group: 'VPN' },
    { name: 'VPN Providers', path: '/vpn/providers', icon: Server, group: 'VPN' },
    {
      name: 'Log & Report',
      path: '/log',
      icon: Activity,
      group: 'Log & Report'
    },
    { name: 'Live Events', path: '/events', icon: Rss, group: 'Log & Report' },
    {
      name: 'System',
      path: '/system',
      icon: Settings,
      group: 'System'
    },
    { name: 'Revisions', path: '/system/revisions', icon: History, group: 'System' },
    { name: 'Notifications', path: '/system/notifications', icon: Bell, group: 'System' },
    { name: 'Plugins', path: '/system/plugins', icon: Puzzle, group: 'System' },
    { name: 'Updates', path: '/system/updates', icon: Download, group: 'System' },
    { name: 'Snapshots', path: '/system/snapshots', icon: Camera, group: 'System' }
  ]

  if (sentryInstalled) {
    navItems.push(
      { name: 'Queue', path: '/plugins/sentry', icon: Inbox, group: 'Goblin SOC' },
      { name: 'Incidents', path: '/plugins/sentry/incidents', icon: ScrollText, group: 'Goblin SOC' },
      { name: 'Rules', path: '/plugins/sentry/rules', icon: ListOrdered, group: 'Goblin SOC' },
      { name: 'Blocks', path: '/plugins/sentry/blocks', icon: Ban, group: 'Goblin SOC' }
    )
  }

  // Group navigation items
  const groups = Array.from(
    new Map(navItems.map(item => [item.group, []])).keys()
  )

  return (
    <div className="flex h-screen overflow-x-hidden bg-gray-50 dark:bg-transparent">
      {/* Sidebar: part of the layout from md up, a slide-in drawer below it.
          The drawer holds its own scrolling and is not a scroll box, so the
          sticky nav inside it is not swallowed by a clipping ancestor. */}
      <aside
        id="sidebar"
        aria-label="Main navigation"
        aria-hidden={phone ? !isMobileMenuOpen : undefined}
        data-testid="sidebar"
        className={`fixed md:sticky inset-y-0 left-0 z-[60] md:z-auto flex w-64 flex-shrink-0 transform flex-col bg-white shadow-lg transition-transform duration-300 ease-in-out dark:bg-gray-900/80 dark:backdrop-blur md:translate-x-0 md:shadow-none dark:border-r dark:border-gray-700 ${
          isMobileMenuOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'
        }`}
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
            type="button"
            onClick={() => setIsMobileMenuOpen(false)}
            aria-label="Close menu"
            className="flex h-10 w-10 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200 md:hidden"
          >
            <X size={20} />
          </button>
        </div>

        {/* Sidebar content */}
        <div className="min-h-0 flex-1 overflow-y-auto py-4">
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
                        className={`flex min-h-[40px] items-center px-4 py-2 text-sm font-medium rounded-lg transition-colors ${
                          isActive
                            ? 'bg-blue-100 text-blue-700 dark:bg-gray-800 dark:text-blue-400'
                            : 'text-gray-700 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100'
                        }`}
                      >
                        <Icon size={18} className="mr-3" />
                        <span className="flex-1">{item.name}</span>
                        {updateAvailable && (item.path === '/system' || item.path === '/system/updates') && (
                          <span className="ml-2 h-2 w-2 rounded-full bg-green-500" title="Update available" />
                        )}
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
              aria-label="Switch colour theme"
              className="flex h-10 w-10 items-center justify-center rounded-full bg-gray-200 text-gray-700 hover:bg-gray-300 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              {document.documentElement.classList.contains('dark') ? '☀️' : '🌙'}
            </button>
          </div>
        </div>
      </aside>

      {/* Dim the page while the drawer is open. The drawer sits above the
          header; the part of the header and the phone commit bar right of the
          drawer stays above the dimming, so commit is reachable with the menu open. */}
      {isMobileMenuOpen && (
        <div
          className="fixed inset-0 z-30 bg-black bg-opacity-50 md:hidden"
          onClick={() => setIsMobileMenuOpen(false)}
        ></div>
      )}

      {/* Main content */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        {/* Top bar */}
        <header className="relative z-50 flex-shrink-0 bg-white dark:bg-gray-900/80 dark:backdrop-blur border-b border-gray-200 dark:border-gray-700 shadow-sm">
          <div className="flex items-center justify-between px-2 py-2 md:px-4 md:py-3">
            <div className="flex min-w-0 items-center">
              <button
                type="button"
                onClick={() => setIsMobileMenuOpen((open) => !open)}
                aria-label={isMobileMenuOpen ? 'Close menu' : 'Open menu'}
                aria-expanded={isMobileMenuOpen}
                aria-controls="sidebar"
                className="mr-1 flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200 md:hidden"
              >
                <Menu size={20} />
              </button>
              <h1 className="truncate text-base font-semibold text-gray-800 dark:text-white md:text-lg">Goblin Gate</h1>
            </div>
            <div className="flex items-center space-x-1 md:space-x-4">
              <ConnectionBadge />
              <div className="hidden md:block">
                <CommitBar />
              </div>
              <button
                onClick={() => setIsSearchOpen(true)}
                title="Global search (Ctrl+K)"
                aria-label="Global search"
                className="flex h-10 items-center gap-2 rounded-md bg-gray-100 px-3 text-sm text-gray-700 hover:bg-gray-200 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                <Search size={18} />
                <span className="hidden md:inline">Search</span>
              </button>
              <Link
                to="/log"
                title="Log & Report"
                aria-label="Log & Report"
                className="hidden h-10 w-10 items-center justify-center rounded-full bg-gray-200 text-gray-700 hover:bg-gray-300 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 sm:flex"
              >
                <Activity size={20} />
              </Link>
              <UserMenu />
            </div>
          </div>
        </header>

        {/* Commit bar for phones (the header one is md and up only) */}
        <div className="relative z-50 flex-shrink-0 overflow-x-auto bg-white dark:bg-gray-900/80 dark:backdrop-blur border-b border-gray-200 dark:border-gray-700 shadow-sm md:hidden">
          <div className="px-2 py-2">
            <CommitBar />
          </div>
        </div>

        {/* Page content */}
        <main className="flex-1 overflow-y-auto p-4 md:p-6 bg-gray-50 dark:bg-transparent">
          <div className="max-w-7xl mx-auto">
            <div className="bg-white dark:bg-gray-800/90 border border-gray-200 dark:border-gray-700 rounded-xl shadow-sm p-3 md:p-6">
              <PageErrorBoundary resetKey={location.pathname}>
                <Outlet />
              </PageErrorBoundary>
            </div>
          </div>
        </main>
      </div>

      {/* Search Modal */}
      <SearchModal isOpen={isSearchOpen} onClose={() => setIsSearchOpen(false)} />
    </div>
  )
}

export default Layout