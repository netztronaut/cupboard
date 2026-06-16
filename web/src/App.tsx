import { useEffect, useState } from 'react'
import type { User } from 'oidc-client-ts'
import './App.css'
import { clearUserSession, currentUser, getAuthConfig, handleAuthCallback, loginWithPKCE } from './auth'

const AUTO_SIGN_IN_FAILURE_LIMIT = 3
const AUTO_SIGN_IN_FAILURE_COUNT_KEY = 'cupboard.auth.autoSignInFailures'

let autoSignInPromise: Promise<void> | undefined

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

function autoSignInFailureCount(): number {
  const value = window.sessionStorage.getItem(AUTO_SIGN_IN_FAILURE_COUNT_KEY)
  if (!value) {
    return 0
  }
  const count = Number.parseInt(value, 10)
  return Number.isFinite(count) && count > 0 ? count : 0
}

function recordAutoSignInFailure(): number {
  const count = autoSignInFailureCount() + 1
  window.sessionStorage.setItem(AUTO_SIGN_IN_FAILURE_COUNT_KEY, String(count))
  return count
}

function resetAutoSignInFailures() {
  window.sessionStorage.removeItem(AUTO_SIGN_IN_FAILURE_COUNT_KEY)
}

function autoSignInLoopError(): Error {
  return new Error(
    `automatic sign-in failed ${AUTO_SIGN_IN_FAILURE_LIMIT} times in a row; not redirecting again to avoid a sign-in loop`,
  )
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

  const loadBackendSessionSubject = async (): Promise<boolean> => {
    const response = await fetch('/api/session', {
      credentials: 'include',
    })
    if (!response.ok) {
      return false
    }
    const session = (await response.json()) as { userInfo?: Record<string, unknown> }
    const sub = session.userInfo?.sub
    if (typeof sub === 'string') {
      setSubject(sub)
    }
    return true
  }

  const validCurrentUser = async (): Promise<User | null> => {
    const user = await currentUser()
    if (user?.expired) {
      await clearUserSession()
      return null
    }
    return user
  }

  const startAutomaticSignIn = async () => {
    if (autoSignInFailureCount() >= AUTO_SIGN_IN_FAILURE_LIMIT) {
      throw autoSignInLoopError()
    }
    if (autoSignInPromise) {
      return autoSignInPromise
    }
    autoSignInPromise = (async () => {
      await loginWithPKCE()
    })().catch((err: unknown) => {
      autoSignInPromise = undefined
      if (recordAutoSignInFailure() >= AUTO_SIGN_IN_FAILURE_LIMIT) {
        throw autoSignInLoopError()
      }
      throw err
    })
    return autoSignInPromise
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

        const redirectPath = authConfig.redirectPath || '/auth/callback'
        const isCallback = window.location.pathname === redirectPath
        let user: User | null = null

        if (isCallback) {
          try {
            user = await handleAuthCallback()
          } catch {
            window.history.replaceState({}, '', '/')
            if (recordAutoSignInFailure() >= AUTO_SIGN_IN_FAILURE_LIMIT) {
              throw autoSignInLoopError()
            }
            await startAutomaticSignIn()
            return
          }
          window.history.replaceState({}, '', '/')
        } else {
          user = await validCurrentUser()
        }

        if (user) {
          try {
            await authenticateBackend(user)
          } catch {
            await clearUserSession()
            if (recordAutoSignInFailure() >= AUTO_SIGN_IN_FAILURE_LIMIT) {
              throw autoSignInLoopError()
            }
            await startAutomaticSignIn()
            return
          }
          resetAutoSignInFailures()
          window.location.replace('/')
          return
        }

        if (!user) {
          if (await loadBackendSessionSubject()) {
            resetAutoSignInFailures()
            window.location.replace('/')
            return
          }
          await startAutomaticSignIn()
          return
        }

      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      } finally {
        setLoading(false)
      }
    })()
  }, [])

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
            <small>signed in as {subject}</small>
          ) : loading ? (
            <small>signing in…</small>
          ) : error ? (
            <small>sign-in unavailable</small>
          ) : (
            <small>sign-in required</small>
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
