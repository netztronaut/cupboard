import { useEffect, useState } from 'react'
import type { User } from 'oidc-client-ts'
import './App.css'
import { clearUserSession, currentUser, getAuthConfig, handleAuthCallback, loginWithPKCE } from './auth'

type DashboardLink = {
  name: string
  url: string
  target?: string
  icon?: string
  source?: string
}

type DashboardGroup = {
  name: string
  links: DashboardLink[]
}

type DashboardResponse = {
  groups: DashboardGroup[]
}

function App() {
  const [groups, setGroups] = useState<DashboardGroup[]>([])
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [subject, setSubject] = useState<string>()
  const [authEnabled, setAuthEnabled] = useState(true)

  const fetchDashboard = async (token?: string) => {
    const response = await fetch('/api/dashboard', {
      credentials: 'include',
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
    if (!response.ok) {
      throw new Error(`failed to load dashboard (${response.status})`)
    }
    const data = (await response.json()) as DashboardResponse
    setGroups(data.groups)
  }

  const authenticateBackend = async (user?: User | null) => {
    if (!user?.access_token) {
      return
    }
    const response = await fetch('/api/session', {
      method: 'POST',
      credentials: 'include',
      headers: {
        Authorization: `Bearer ${user.access_token}`,
      },
    })
    if (!response.ok) {
      throw new Error(`backend auth failed (${response.status})`)
    }
    const session = (await response.json()) as { userInfo?: Record<string, unknown> }
    const sub = session.userInfo?.sub
    if (typeof sub === 'string') {
      setSubject(sub)
    }
  }

  useEffect(() => {
    ;(async () => {
      try {
        const authConfig = await getAuthConfig()
        setAuthEnabled(authConfig.enabled)
        if (!authConfig.enabled) {
          setSubject('anonymous')
          await fetchDashboard()
          return
        }

        const isCallback = window.location.pathname === '/auth/callback'
        if (isCallback) {
          await handleAuthCallback()
          window.history.replaceState({}, '', '/')
        }

        const user = await currentUser()
        await authenticateBackend(user)

        if (!user) {
          const response = await fetch('/api/session', {
            credentials: 'include',
          })
          if (response.ok) {
            const session = (await response.json()) as { userInfo?: Record<string, unknown> }
            const sub = session.userInfo?.sub
            if (typeof sub === 'string') {
              setSubject(sub)
            }
          }
        }

        await fetchDashboard(user?.access_token)
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  const onLogin = async () => {
    try {
      await loginWithPKCE()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      setError(message)
    }
  }

  const onLogout = async () => {
    await clearUserSession()
    await fetch('/api/session', {
      method: 'DELETE',
      credentials: 'include',
    })
    setGroups([])
    setSubject(undefined)
  }

  const isImageIcon = (icon?: string) =>
    !!icon &&
    (icon.startsWith('http://') ||
      icon.startsWith('https://') ||
      icon.startsWith('data:') ||
      icon.startsWith('/') ||
      icon.startsWith('./') ||
      icon.startsWith('../'))

  return (
    <main className="app">
      <header>
        <h1>cupboard</h1>
        <p className="subtitle">Kubernetes operator control surface</p>
        <div className="actions">
          {!authEnabled ? (
            <small>authentication disabled</small>
          ) : subject ? (
            <>
              <small>signed in as {subject}</small>
              <button onClick={onLogout}>Log out</button>
            </>
          ) : (
            <button onClick={onLogin}>Sign in (PKCE)</button>
          )}
        </div>
      </header>

      <section className="panel">
        <h2>Bookmarks</h2>
        {error && <p className="error">{error}</p>}
        {loading && <p>Loading…</p>}
        {!error && groups.length === 0 && <p>No bookmark data found yet.</p>}
        {groups.map((group) => (
          <article key={group.name} className="group">
            <h3>{group.name}</h3>
            <ul>
              {group.links.map((link) => (
                <li key={`${group.name}-${link.name}-${link.url}`}>
                  <a href={link.url} target={link.target || '_self'} rel="noreferrer">
                    {isImageIcon(link.icon) ? <img src={link.icon} alt="" /> : link.icon ? <small>{link.icon}</small> : null}
                    <span>{link.name}</span>
                  </a>
                  {link.source && <small>{link.source}</small>}
                </li>
              ))}
            </ul>
          </article>
        ))}
      </section>
    </main>
  )
}

export default App
