package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:embed templates/**/*.tmpl
var templateFS embed.FS

type pageTemplate struct {
	tmpl *template.Template
	set  string
}

type pageTemplateData struct {
	Title         string
	FaviconURL    string
	ContentLayout string
	Groups        []DashboardGroup
}

var iconNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadPageTemplate(set string) (*pageTemplate, error) {
	selectedSet := strings.TrimSpace(set)
	if selectedSet == "" {
		selectedSet = "default"
	}

	parsed, err := template.New("page").Funcs(template.FuncMap{
		"isFontAwesomeKey": func(value string) bool {
			return strings.HasPrefix(strings.TrimSpace(value), "fa-")
		},
		"lower": func(value string) string {
			return strings.ToLower(strings.TrimSpace(value))
		},
		"urlHost": func(value string) string {
			raw := strings.TrimSpace(value)
			if raw == "" {
				return ""
			}
			parsed, err := url.Parse(raw)
			if err != nil || parsed.Host == "" {
				return raw
			}
			host := strings.TrimPrefix(parsed.Hostname(), "www.")
			if host == "" {
				return parsed.Host
			}
			return host
		},
		"initial": func(value string) string {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				return "?"
			}
			r, _ := utf8.DecodeRuneInString(trimmed)
			if r == utf8.RuneError {
				return "?"
			}
			return strings.ToUpper(string(r))
		},
		"iconAssetURL": func(value string) string {
			raw := strings.TrimSpace(value)
			prefix, name, found := strings.Cut(raw, ":")
			if !found || !iconNamePattern.MatchString(name) {
				return ""
			}
			switch prefix {
			case "lucide":
				return "/static/icons/lucide/" + name + ".svg"
			case "tabler":
				return "/static/icons/tabler/" + name + ".svg"
			case "hero":
				return "/static/icons/heroicons/24/outline/" + name + ".svg"
			default:
				return ""
			}
		},
	}).ParseFS(templateFS,
		fmt.Sprintf("templates/%s/page.tmpl", selectedSet),
		fmt.Sprintf("templates/%s/header.tmpl", selectedSet),
		fmt.Sprintf("templates/%s/footer.tmpl", selectedSet),
		fmt.Sprintf("templates/%s/content.tmpl", selectedSet),
		fmt.Sprintf("templates/%s/group.tmpl", selectedSet),
		fmt.Sprintf("templates/%s/link.tmpl", selectedSet),
	)
	if err != nil {
		return nil, err
	}

	return &pageTemplate{tmpl: parsed, set: selectedSet}, nil
}
