import { UserManager, WebStorageStateStore, type User } from 'oidc-client-ts'

export type AuthConfig = {
  enabled: boolean
  issuerUrl?: string
  openidConfigurationUrl?: string
  clientId?: string
  redirectPath?: string
  scopes?: string
}

declare global {
  interface Window {
    config?: AuthConfig
  }
}

let managerPromise: Promise<UserManager> | undefined

export async function getAuthConfig(): Promise<AuthConfig> {
  if (!window.config) {
    throw new Error('auth config missing from root document')
  }
  return window.config
}

async function requireEnabledAuthConfig(): Promise<AuthConfig> {
  const config = await getAuthConfig()
  if (!config.enabled) {
    throw new Error('authentication is disabled on backend')
  }
  return config
}

export async function getUserManager(): Promise<UserManager> {
  if (!managerPromise) {
    managerPromise = requireEnabledAuthConfig().then((config) => {
      if (!config.issuerUrl || !config.clientId) {
        throw new Error('OIDC auth is not configured on backend (missing OIDC_ISSUER_URL or OIDC_CLIENT_ID)')
      }
      return new UserManager({
        authority: config.issuerUrl,
        metadataUrl: config.openidConfigurationUrl || undefined,
        client_id: config.clientId,
        redirect_uri: `${window.location.origin}${config.redirectPath || '/auth/callback'}`,
        response_type: 'code',
        scope: config.scopes || 'openid profile email',
        userStore: new WebStorageStateStore({ store: window.localStorage }),
      })
    })
  }
  return managerPromise
}

export async function loginWithPKCE(): Promise<void> {
  const manager = await getUserManager()
  await manager.signinRedirect()
}

export async function handleAuthCallback(): Promise<User> {
  const manager = await getUserManager()
  return manager.signinRedirectCallback()
}

export async function currentUser(): Promise<User | null> {
  const manager = await getUserManager()
  return manager.getUser()
}

export async function clearUserSession(): Promise<void> {
  const manager = await getUserManager()
  await manager.removeUser()
}
