import type { PageConfig } from './auth'

// These mirror the Go template helper funcs registered in web/templates.go
// (loadPageTemplate's template.FuncMap) so the SPA's icon rendering matches
// the server-rendered theme templates exactly.

export function isFontAwesomeKey(value?: string): boolean {
  return !!value && value.trim().startsWith('fa-')
}

export function iconAssetURL(value?: string): string {
  const raw = (value ?? '').trim()
  const [prefix, name] = raw.split(':')
  if (!name || !/^[a-z0-9-]+$/.test(name)) return ''
  switch (prefix) {
    case 'lucide':
      return `/static/icons/lucide/${name}.svg`
    case 'tabler':
      return `/static/icons/tabler/${name}.svg`
    case 'hero':
      return `/static/icons/heroicons/24/outline/${name}.svg`
    default:
      return ''
  }
}

export function urlHost(raw?: string): string {
  const value = (raw ?? '').trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    return (parsed.hostname || parsed.host || value).replace(/^www\./, '')
  } catch {
    return value
  }
}

export function initial(value?: string): string {
  const trimmed = (value ?? '').trim()
  return trimmed ? trimmed.charAt(0).toUpperCase() : '?'
}

export type TemplateSet = 'default' | 'forecastle'

// isCustomTemplateSet reports whether the configured template set is one the
// SPA has no React port for — an operator-supplied, filesystem-loaded set
// (see web/handler.go's readPageTemplateFile / README's template-set
// resolution order). Arbitrary Go templates can't be introspected from the
// browser, so those don't get a matching React view; see hasServerRenderedTheme
// in App.tsx for how the caller should handle this.
export function isCustomTemplateSet(set?: string): boolean {
  return !!set && set !== 'default' && set !== 'forecastle'
}

function resolveTemplateSet(set?: string): TemplateSet {
  return set === 'forecastle' ? 'forecastle' : 'default'
}

let currentThemeStylesheetId: string | undefined

function ensureStylesheet(id: string, href: string) {
  if (document.getElementById(id)) return
  const link = document.createElement('link')
  link.id = id
  link.rel = 'stylesheet'
  link.href = href
  document.head.appendChild(link)
}

// applyPageTheme links the CSS for the configured template set (the same
// stylesheets web/templates/<set>/page.tmpl links) and sets the document
// title/favicon to match. Callers must only invoke this once they are ready
// to render real content — never while auth is still being resolved — so
// the browser never paints a themed-but-empty shell before the situation
// is clear. Returns which set was applied, for the caller to pick a view.
export function applyPageTheme(page: PageConfig | undefined): TemplateSet {
  const set = resolveTemplateSet(page?.templateSet)
  const stylesheetId = `cupboard-theme-${set}`

  ensureStylesheet('cupboard-fontawesome-base', '/static/fontawesome/css/fontawesome.min.css')
  ensureStylesheet('cupboard-fontawesome-brands', '/static/fontawesome/css/brands.min.css')
  ensureStylesheet('cupboard-fontawesome-solid', '/static/fontawesome/css/solid.min.css')

  if (currentThemeStylesheetId && currentThemeStylesheetId !== stylesheetId) {
    document.getElementById(currentThemeStylesheetId)?.remove()
  }
  ensureStylesheet(stylesheetId, `/static/themes/${set}.css`)
  currentThemeStylesheetId = stylesheetId

  if (page?.title) {
    document.title = page.title
  }
  if (page?.faviconUrl) {
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.href = page.faviconUrl
  }

  return set
}
