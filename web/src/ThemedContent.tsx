import { useLayoutEffect, useRef, useState } from 'react'
import type { DashboardGroup, DashboardInfoTile, DashboardLink } from './types'
import { initial, iconAssetURL, isFontAwesomeKey, urlHost, type TemplateSet } from './theme'

// The two views below mirror web/templates/default and web/templates/forecastle
// (see the .tmpl files under web/templates/) so an authenticated SPA session
// looks the same as a direct hit to "/" that already carries a valid session
// cookie. Only built-in sets are reproduced here — a custom, filesystem-provided
// template set configured via Page.TemplateSet falls back to the default look,
// since arbitrary Go templates can't be introspected from the browser.

export type ThemedContentProps = {
  templateSet: TemplateSet
  title: string
  contentLayout: string
  groups: DashboardGroup[]
  authEnabled: boolean
  subject?: string
  error?: string
}

export function ThemedContent(props: ThemedContentProps) {
  if (props.templateSet === 'forecastle') {
    return <ForecastleThemeView {...props} />
  }
  return <DefaultThemeView {...props} />
}

function statusText(authEnabled: boolean, subject?: string, error?: string): string | undefined {
  if (!authEnabled) return undefined
  if (error) return 'sign-in unavailable'
  if (subject) return `signed in as ${subject}`
  return 'sign-in required'
}

function DefaultIcon({ icon }: { icon?: string }) {
  if (!icon) return null
  if (isFontAwesomeKey(icon)) return <i className={`icon fa-brands ${icon}`} aria-hidden="true" />
  const asset = iconAssetURL(icon)
  if (asset) return <img className="icon icon-image" src={asset} alt="" />
  return <span className="icon">{icon}</span>
}

function DefaultThemeView({ title, contentLayout, groups, authEnabled, subject, error }: ThemedContentProps) {
  const status = statusText(authEnabled, subject, error)
  return (
    <>
      <header>
        <div className="container">
          <h1>
            <img
              src="/static/logo.svg"
              alt=""
              width={24}
              height={24}
              style={{ verticalAlign: 'middle', marginRight: '.4em', imageRendering: 'pixelated' }}
            />
            {title}
          </h1>
          {status && (
            <p>
              <small>{status}</small>
            </p>
          )}
        </div>
      </header>
      <main className={`container content--${contentLayout}`}>
        {error && <p className="error">{error}</p>}
        {!error && groups.length === 0 && <p>No links available.</p>}
        {groups.map((group) => (
          <section className="group" key={group.name}>
            <h2>{group.name}</h2>
            <ul>
              {(group.links ?? []).map((link) => (
                <li key={`${group.name}-${link.name}-${link.url}`}>
                  <a href={link.url} target={link.target || '_self'} rel="noreferrer">
                    <DefaultIcon icon={link.icon} />
                    {link.name}
                  </a>
                </li>
              ))}
              {(group.tiles ?? []).map((tile) => (
                <li className="tile" key={`${group.name}-tile-${tile.name}`}>
                  {tile.url ? (
                    <a href={tile.url} target={tile.target || '_self'} rel="noreferrer">
                      <strong>{tile.name}</strong>
                      {tile.content && (
                        // content is trusted HTML rendered by the operator's Processor template
                        // eslint-disable-next-line react/no-danger
                        <div className="tile-content" dangerouslySetInnerHTML={{ __html: tile.content }} />
                      )}
                    </a>
                  ) : (
                    <>
                      <strong>{tile.name}</strong>
                      {tile.content && (
                        // eslint-disable-next-line react/no-danger
                        <div className="tile-content" dangerouslySetInnerHTML={{ __html: tile.content }} />
                      )}
                    </>
                  )}
                  {tile.source && (
                    // eslint-disable-next-line react/no-danger
                    <span className="source" dangerouslySetInnerHTML={{ __html: tile.source }} />
                  )}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </main>
      <footer>
        <div className="container">
          <small>Served by cupboard</small>
        </div>
      </footer>
    </>
  )
}

type ThemePreference = 'system' | 'light' | 'dark'
const THEME_STORAGE_KEY = 'cupboard-theme'
const THEME_ORDER: ThemePreference[] = ['system', 'light', 'dark']

function readStoredThemePreference(): ThemePreference {
  const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
  return stored === 'light' || stored === 'dark' ? stored : 'system'
}

function ForecastleIcon({ icon, fallbackClass, name }: { icon?: string; fallbackClass: string; name?: string }) {
  if (icon) {
    if (isFontAwesomeKey(icon)) return <i className={`fa-brands ${icon}`} aria-hidden="true" />
    const asset = iconAssetURL(icon)
    if (asset) return <img className="fc-card-icon-image" src={asset} alt="" />
    return <>{initial(name)}</>
  }
  return <i className={`fa-solid ${fallbackClass}`} aria-hidden="true" />
}

function sourceBadge(source?: string) {
  if (source === 'forecastleapp') {
    return (
      <span className="fc-source">
        <i className="fa-solid fa-cubes" aria-hidden="true" /> Forecastle
      </span>
    )
  }
  if (source === 'bookmarkgroup') {
    return (
      <span className="fc-source">
        <i className="fa-solid fa-cube" aria-hidden="true" /> CRD
      </span>
    )
  }
  return (
    <span className="fc-source">
      <i className="fa-solid fa-sliders" aria-hidden="true" /> Config
    </span>
  )
}

function ForecastleLinkCard({ link }: { link: DashboardLink }) {
  return (
    <article className="fc-card">
      <a className="fc-card-main" href={link.url} target={link.target || '_self'} rel="noreferrer">
        <span className="fc-card-icon">
          <ForecastleIcon icon={link.icon} fallbackClass="fa-link" name={link.name} />
        </span>
        <h3 className="fc-card-title">{link.name}</h3>
        <p className="fc-card-url">{urlHost(link.url)}</p>
      </a>
      <div className="fc-card-actions">
        {sourceBadge(link.source)}
        <a className="fc-open" href={link.url} target="_blank" rel="noreferrer" aria-label="Open in new tab">
          <i className="fa-solid fa-arrow-up-right-from-square" aria-hidden="true" />
        </a>
      </div>
    </article>
  )
}

function ForecastleTileCard({ tile }: { tile: DashboardInfoTile }) {
  const mainContent = (
    <>
      <span className="fc-card-icon">
        <ForecastleIcon icon={tile.icon} fallbackClass="fa-chart-line" name={tile.name} />
      </span>
      <h3 className="fc-card-title">{tile.name}</h3>
      {tile.content && (
        // content is trusted HTML rendered by the operator's Processor template
        // eslint-disable-next-line react/no-danger
        <div className="fc-tile-content" dangerouslySetInnerHTML={{ __html: tile.content }} />
      )}
    </>
  )
  return (
    <article className="fc-card fc-tile">
      {tile.url ? (
        <a className="fc-card-main" href={tile.url} target={tile.target || '_self'} rel="noreferrer">
          {mainContent}
        </a>
      ) : (
        <div className="fc-card-main">{mainContent}</div>
      )}
      <div className="fc-card-actions">
        {tile.source ? (
          // eslint-disable-next-line react/no-danger
          <span className="fc-source" dangerouslySetInnerHTML={{ __html: tile.source }} />
        ) : (
          <span className="fc-source">
            <i className="fa-solid fa-chart-bar" aria-hidden="true" /> InfoTile
          </span>
        )}
        {tile.url && (
          <a className="fc-open" href={tile.url} target="_blank" rel="noreferrer" aria-label="Open in new tab">
            <i className="fa-solid fa-arrow-up-right-from-square" aria-hidden="true" />
          </a>
        )}
      </div>
    </article>
  )
}

function ForecastleThemeView({ title, groups, authEnabled, subject, error }: ThemedContentProps) {
  const [query, setQuery] = useState('')
  const [themePref, setThemePref] = useState<ThemePreference>(readStoredThemePreference)
  const searchRef = useRef<HTMLInputElement>(null)

  useLayoutEffect(() => {
    const media = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null
    const apply = () => {
      const effective = themePref === 'system' ? (media?.matches ? 'dark' : 'light') : themePref
      document.documentElement.setAttribute('data-theme', effective)
      document.documentElement.setAttribute('data-theme-preference', themePref)
    }
    apply()
    if (themePref === 'system' && media?.addEventListener) {
      media.addEventListener('change', apply)
      return () => media.removeEventListener('change', apply)
    }
    return undefined
  }, [themePref])

  useLayoutEffect(() => {
    const onKeydown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    document.addEventListener('keydown', onKeydown)
    return () => document.removeEventListener('keydown', onKeydown)
  }, [])

  const cycleTheme = () => {
    const next = THEME_ORDER[(THEME_ORDER.indexOf(themePref) + 1) % THEME_ORDER.length]
    window.localStorage.setItem(THEME_STORAGE_KEY, next)
    setThemePref(next)
  }
  const themeIconClass =
    themePref === 'dark' ? 'fa-moon' : themePref === 'light' ? 'fa-sun' : 'fa-circle-half-stroke'

  const normalizedQuery = query.trim().toLowerCase()
  const matches = (haystack: string) => !normalizedQuery || haystack.toLowerCase().includes(normalizedQuery)
  const status = statusText(authEnabled, subject, error)

  return (
    <>
      <header className="fc-header">
        <div className="container fc-toolbar">
          <div className="fc-brand">
            <span className="fc-brand-mark">
              <img src="/static/logo.svg" alt="" width={20} height={20} style={{ imageRendering: 'pixelated' }} />
            </span>
            <h1 className="fc-brand-title">{title}</h1>
          </div>
          <label className="fc-search" aria-label="Search applications">
            <i className="fa-solid fa-magnifying-glass search-icon" aria-hidden="true" />
            <input
              ref={searchRef}
              type="search"
              placeholder="Search apps..."
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <span className="search-shortcut">⌘K</span>
          </label>
          <div className="fc-toolbar-right">
            {status && <span className="fc-sync">{status}</span>}
            <button
              id="fc-theme-toggle"
              className="fc-theme-toggle"
              type="button"
              aria-label={`Theme: ${themePref}`}
              title={`Theme: ${themePref}`}
              onClick={cycleTheme}
            >
              <i className={`fa-solid ${themeIconClass}`} aria-hidden="true" />
            </button>
          </div>
        </div>
      </header>
      <main className="fc-main">
        {error && (
          <div className="container">
            <p className="error">{error}</p>
          </div>
        )}
        {!error && groups.length === 0 && (
          <div className="container">
            <section className="fc-group">
              <div className="fc-group-panel">No links available.</div>
            </section>
          </div>
        )}
        <div className="container fc-groups">
          {groups.map((group) => {
            const links = group.links ?? []
            const tiles = group.tiles ?? []
            return (
              <section className="fc-group" key={group.name}>
                <details open>
                  <summary className="fc-group-summary">
                    <span className="fc-group-meta">
                      <i className="fa-solid fa-folder-open" aria-hidden="true" />
                      <h2 className="fc-group-name">{group.name}</h2>
                      <span className="fc-group-count">{links.length + tiles.length}</span>
                    </span>
                    <i className="fa-solid fa-chevron-down fc-group-chevron" aria-hidden="true" />
                  </summary>
                  <div className="fc-group-panel">
                    <div className="fc-cards">
                      {links
                        .filter((link) => matches(`${link.name} ${link.url}`))
                        .map((link) => (
                          <ForecastleLinkCard key={`${group.name}-${link.name}-${link.url}`} link={link} />
                        ))}
                      {tiles
                        .filter((tile) => matches(tile.name))
                        .map((tile) => (
                          <ForecastleTileCard key={`${group.name}-tile-${tile.name}`} tile={tile} />
                        ))}
                    </div>
                  </div>
                </details>
              </section>
            )
          })}
        </div>
      </main>
      <footer className="fc-footer">
        <div className="container">Served by cupboard</div>
      </footer>
    </>
  )
}
