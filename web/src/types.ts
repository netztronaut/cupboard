export type DashboardLink = {
  name: string
  url: string
  target?: string
  icon?: string
  source?: string
}

export type DashboardInfoTile = {
  name: string
  icon?: string
  url?: string
  target?: string
  source?: string
  content?: string
}

export type DashboardGroup = {
  name: string
  links: DashboardLink[]
  tiles?: DashboardInfoTile[]
}

export type DashboardResponse = {
  groups: DashboardGroup[]
}
