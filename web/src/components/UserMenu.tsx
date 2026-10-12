import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { LogOut, User } from 'lucide-react'
import { getMe, logout, type Me } from '../api/auth'

// UserMenu shows who is signed in and a sign-out button. In --dev mode
// (no authentication) the server reports a stand-in "dev" user.
export default function UserMenu() {
  const [me, setMe] = useState<Me | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    getMe().then(setMe).catch(() => setMe(null))
  }, [])

  const signOut = async () => {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
      <span className="inline-flex items-center gap-1 rounded-full bg-gray-200 px-3 py-1 dark:bg-gray-700">
        <User size={16} />
        <span>{me?.user ?? '…'}</span>
        {me?.role === 'readonly' && <span className="text-xs text-gray-500">(read-only)</span>}
      </span>
      {me && me.method === 'session' && (
        <button
          type="button"
          onClick={signOut}
          aria-label="Sign out"
          title="Sign out"
          className="rounded-full bg-gray-200 p-2 hover:bg-gray-300 dark:bg-gray-700 dark:hover:bg-gray-600"
        >
          <LogOut size={18} />
        </button>
      )}
    </div>
  )
}
