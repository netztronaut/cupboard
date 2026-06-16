package web

type Options struct {
	Auth        AuthOptions
	Forecastle  ForecastleOptions
	LinkGroups  []LinkGroup
	StaticLinks []StaticLink
	Page        PageOptions
}

type AuthOptions struct {
	Enabled             bool
	CookieSecret        string
	IssuerURL           string
	ClientID            string
	RedirectPath        string
	Scopes              string
	UserInfoEndpointURL string
}

type ForecastleOptions struct {
	Instance string
}

type StaticLink struct {
	Group     string
	LinkGroup string
	Name      string
	URL       string
	Target    string
	Icon      string
	Groups    []string
}

type PageOptions struct {
	TemplateSet   string
	Title         string
	FaviconURL    string
	ContentLayout string
}

type LinkGroup struct {
	Name          string
	Priority      int
	PriorityClass string
	DisplayName   string
}
