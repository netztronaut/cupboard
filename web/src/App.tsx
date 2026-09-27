import { useEffect, useState } from 'react'
import type { User } from 'oidc-client-ts'
import './App.css'
import { clearUserSession, currentUser, getAuthConfig, handleAuthCallback, loginWithPKCE, type PageConfig } from './auth'
import { ThemedContent } from './ThemedContent'
import { applyPageTheme, isCustomTemplateSet, type TemplateSet } from './theme'
import type { DashboardResponse, DashboardGroup } from './types'

const SIGN_IN_BACKOFF_KEY = 'cupboard.auth.signInBackoff'
const SIGN_IN_ATTEMPT_KEY = 'cupboard.auth.signInAttempts'
const SIGN_IN_BACKOFF_INITIAL_MS = 5_000
const SIGN_IN_BACKOFF_MAX_MS = 60_000
const HANDOFF_KEY = 'cupboard.auth.handoffAt'
const HANDOFF_LOOP_WINDOW_MS = 15_000

let autoSignInPromise: Promise<void> | undefined

function getSignInBackoffMs(): number {
  const raw = window.sessionStorage.getItem(SIGN_IN_BACKOFF_KEY)
  const ms = Number.parseInt(raw ?? '', 10)
  return Number.isFinite(ms) && ms > 0 ? ms : 0
}

function getSignInAttempts(): number {
  const raw = window.sessionStorage.getItem(SIGN_IN_ATTEMPT_KEY)
  const n = Number.parseInt(raw ?? '', 10)
  return Number.isFinite(n) && n > 0 ? n : 0
}

function recordSignInFailure(): number {
  const current = getSignInBackoffMs()
  const next = current === 0 ? SIGN_IN_BACKOFF_INITIAL_MS : Math.min(current * 2, SIGN_IN_BACKOFF_MAX_MS)
  window.sessionStorage.setItem(SIGN_IN_BACKOFF_KEY, String(next))
  const attempts = getSignInAttempts() + 1
  window.sessionStorage.setItem(SIGN_IN_ATTEMPT_KEY, String(attempts))
  return next
}

function resetSignInBackoff() {
  window.sessionStorage.removeItem(SIGN_IN_BACKOFF_KEY)
  window.sessionStorage.removeItem(SIGN_IN_ATTEMPT_KEY)
}

// handOffToServerPage navigates to "/", which the server renders from the
// configured template set for the current session — the exact view a manual
// reload would show, filtered by the signed-in user's groups. Never resolves:
// the splash stays up until the browser has navigated away.
//
// If the SPA is served for "/" again right after a hand-off, the server did
// not accept the session cookie (e.g. the browser dropped it). Redirecting
// again would loop forever, so report that instead.
function handOffToServerPage(): Promise<never> {
  const last = Number.parseInt(window.sessionStorage.getItem(HANDOFF_KEY) ?? '', 10)
  window.sessionStorage.removeItem(HANDOFF_KEY)
  if (Number.isFinite(last) && Date.now() - last < HANDOFF_LOOP_WINDOW_MS) {
    throw new Error('signed in, but the server did not accept the session cookie')
  }
  window.sessionStorage.setItem(HANDOFF_KEY, String(Date.now()))
  window.location.replace('/')
  return new Promise<never>(() => {})
}

type SplashProps = {
  retryIn?: number
  signInAttempts: number
  error?: string
}

// Splash is the neutral, theme-independent loading screen shown while auth is
// being resolved. web/index.html carries the same markup so it is visible
// before this bundle has even executed.
function Splash({ retryIn, signInAttempts, error }: SplashProps) {
  if (error) {
    return (
      <div className="splash" role="alert">
        <p className="error">Sign-in failed: {error}</p>
        <button type="button" className="splash-retry" onClick={() => window.location.replace('/')}>
          Try again
        </button>
      </div>
    )
  }
  return (
    <div className="splash" role="status" aria-live="polite">
      <div className="spinner" aria-hidden="true" />
      {retryIn !== undefined ? <p>Retrying sign-in in {retryIn}s…</p> : <p className="visually-hidden">Loading…</p>}
      {signInAttempts >= 3 && (
        <p className="splash-warning">
          Sign-in is taking longer than expected. Please check that the identity provider is reachable and your
          network connection is stable. Retrying automatically…
        </p>
      )}
    </div>
  )
}

function App() {
  const [groups, setGroups] = useState<DashboardGroup[]>([])
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const [subject, setSubject] = useState<string>()
  const [authEnabled, setAuthEnabled] = useState(true)
  const [wsEnabled, setWsEnabled] = useState(false)
  const [retryIn, setRetryIn] = useState<number>()
  const [signInAttempts, setSignInAttempts] = useState(() => getSignInAttempts())
  const [themeSet, setThemeSet] = useState<TemplateSet>('default')
  const [pageTitle, setPageTitle] = useState('cupboard')
  const [contentLayout, setContentLayout] = useState('list')

  // Links/applies the configured template set's CSS and title/favicon. Callers
  // must only invoke this once dashboard content has actually loaded — never
  // while auth is still being resolved — so the browser never shows a themed
  // shell before it's clear whether the user is signed in, and never shows a
  // themed-but-empty page ahead of real content.
  const applyThemeForContent = (page?: PageConfig) => {
    setThemeSet(applyPageTheme(page))
    setPageTitle(page?.title || 'cupboard')
    setContentLayout(page?.contentLayout || 'list')
  }

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
  }

  const hasBackendSession = async (): Promise<boolean> => {
    const response = await fetch('/api/session', {
      credentials: 'include',
    })
    return response.ok
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
    if (autoSignInPromise) return autoSignInPromise
    const backoffMs = getSignInBackoffMs()
    autoSignInPromise = (async () => {
      if (backoffMs > 0) {
        let remaining = Math.ceil(backoffMs / 1000)
        setRetryIn(remaining)
        await new Promise<void>((resolve) => {
          const interval = setInterval(() => {
            remaining -= 1
            if (remaining <= 0) {
              clearInterval(interval)
              setRetryIn(undefined)
              resolve()
            } else {
              setRetryIn(remaining)
            }
          }, 1000)
        })
      }
      await loginWithPKCE()
    })().catch((err: unknown) => {
      autoSignInPromise = undefined
      recordSignInFailure()
      setSignInAttempts(getSignInAttempts())
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
          // For a template set the SPA has no React port for (any
          // operator-supplied, filesystem-loaded set — see isCustomTemplateSet),
          // an approximation here would drift from the real theme; the server
          // renders it exactly.
          if (isCustomTemplateSet(authConfig.page?.templateSet)) {
            await handOffToServerPage()
          }
          await fetchDashboard()
          applyThemeForContent(authConfig.page)
          setWsEnabled(true)
          return
        }

        // With auth enabled the SPA is only an authentication shell: it never
        // renders dashboard content itself. Once the backend session cookie is
        // established it hands off to the server-rendered page, so the user
        // lands directly on the view that matches their identity and groups.
        const redirectPath = authConfig.redirectPath || '/auth/callback'
        const isCallback = window.location.pathname === redirectPath
        let user: User | null = null

        if (isCallback) {
          try {
            user = await handleAuthCallback()
          } catch {
            window.history.replaceState({}, '', '/')
            recordSignInFailure()
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
            recordSignInFailure()
            await startAutomaticSignIn()
            return
          }
          resetSignInBackoff()
          await handOffToServerPage()
        }

        if (await hasBackendSession()) {
          resetSignInBackoff()
          await handOffToServerPage()
        }
        await startAutomaticSignIn()
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        setError(message)
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  useEffect(() => {
    if (!wsEnabled) return

    let destroyed = false
    let ws: WebSocket | null = null
    let retryDelay = 1000
    let retryTimer: ReturnType<typeof setTimeout> | null = null

    const refetch = () => {
      fetch('/api/dashboard', { credentials: 'include' })
        .then((r) => (r.ok ? r.json() : null))
        .then((data) => {
          if (data) setGroups((data as DashboardResponse).groups)
        })
        .catch(() => {})
    }

    const connect = (isReconnect: boolean) => {
      if (destroyed) return
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      ws = new WebSocket(`${proto}//${window.location.host}/api/dashboard/updates`)

      ws.onopen = () => {
        retryDelay = 1000
        if (isReconnect) refetch()
      }

      ws.onmessage = () => {
        refetch()
      }

      ws.onerror = () => {
        ws?.close()
      }

      ws.onclose = () => {
        ws = null
        if (destroyed) return
        retryTimer = setTimeout(() => connect(true), retryDelay)
        retryDelay = Math.min(retryDelay * 2, 30000)
      }
    }

    connect(false)

    return () => {
      destroyed = true
      if (retryTimer !== null) clearTimeout(retryTimer)
      ws?.close()
    }
  }, [wsEnabled])

  // Until content is ready — and, with auth enabled, always, since that flow
  // ends by handing off to the server-rendered page — show only the neutral,
  // theme-independent splash. No template set's stylesheet is linked yet, so
  // nothing half-rendered or wrongly themed is ever painted.
  if (loading || authEnabled) {
    return <Splash retryIn={retryIn} signInAttempts={signInAttempts} error={error} />
  }

  return (
    <ThemedContent
      templateSet={themeSet}
      title={pageTitle}
      contentLayout={contentLayout}
      groups={groups}
      authEnabled={authEnabled}
      subject={subject}
      error={error}
    />
  )
}

export default App
