import { useEffect, useState } from 'react'
import type { User } from 'oidc-client-ts'
import './App.css'
import { clearUserSession, currentUser, getAuthConfig, handleAuthCallback, loginWithPKCE } from './auth'

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
// being resolved — the only thing the SPA ever renders. web/index.html carries the same markup so it is visible
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

// App never renders dashboard content: "/" is always rendered by the server
// from the configured template set. The SPA is only served for "/" when auth
// is enabled and there is no valid session cookie yet (and for other paths
// such as the OIDC callback); its job is to establish that session and then
// hand off to the server-rendered page.
function App() {
  const [error, setError] = useState<string>()
  const [retryIn, setRetryIn] = useState<number>()
  const [signInAttempts, setSignInAttempts] = useState(() => getSignInAttempts())

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
        if (!authConfig.enabled) {
          await handOffToServerPage()
        }

        // Once the backend session cookie is established, hand off to the
        // server-rendered page so the user lands directly on the view that
        // matches their identity and groups.
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
      }
    })()
  }, [])

  return <Splash retryIn={retryIn} signInAttempts={signInAttempts} error={error} />
}

export default App
