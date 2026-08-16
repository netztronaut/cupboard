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

  // For a template set the SPA has no React port for (any operator-supplied,
  // filesystem-loaded set — see isCustomTemplateSet), rendering an approximation
  // here would drift from the real theme. Instead, once auth is resolved, hand
  // off to the same auth-gated server route ("/") that already renders that
  // exact template correctly with the session cookie that was just established.
  // Never resolves: the splash stays up (nothing themed or unthemed is painted)
  // until the browser actually navigates away.
  const handOffToServerTemplate = async (): Promise<never> => {
    window.location.replace('/')
    return new Promise<never>(() => {})
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
          if (isCustomTemplateSet(authConfig.page?.templateSet)) {
            await handOffToServerTemplate()
          }
          await fetchDashboard()
          applyThemeForContent(authConfig.page)
          setWsEnabled(true)
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
          if (isCustomTemplateSet(authConfig.page?.templateSet)) {
            await handOffToServerTemplate()
          }
          await fetchDashboard()
          applyThemeForContent(authConfig.page)
          setWsEnabled(true)
          return
        }

        if (await loadBackendSessionSubject()) {
          resetSignInBackoff()
          if (isCustomTemplateSet(authConfig.page?.templateSet)) {
            await handOffToServerTemplate()
          }
          await fetchDashboard()
          applyThemeForContent(authConfig.page)
          setWsEnabled(true)
          return
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

  // While loading (auth still being resolved, or the first dashboard fetch
  // still in flight) show only this neutral, theme-independent splash — no
  // stylesheet for any template set is linked yet, so there is nothing here
  // for a previously-applied theme to clash with.
  if (loading) {
    return (
      <div className="splash">
        <h1>cupboard</h1>
        {retryIn !== undefined ? <p>Retrying sign-in in {retryIn}s…</p> : <p>Loading…</p>}
        {signInAttempts >= 3 && (
          <p className="splash-warning">
            Sign-in is taking longer than expected. Please check that the identity provider is reachable and your
            network connection is stable. Retrying automatically…
          </p>
        )}
      </div>
    )
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
