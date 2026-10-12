import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { getSetupState } from '../api/setup'

// SetupGate sends the browser to /setup while the box still needs first-boot
// setup. It renders nothing; errors leave the user where they are.
export default function SetupGate() {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  useEffect(() => {
    if (pathname === '/setup') return
    let live = true
    getSetupState()
      .then((s) => {
        if (live && s.needed) navigate('/setup', { replace: true })
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [pathname, navigate])
  return null
}
