package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:embed templates/**/*.tmpl
var templateFS embed.FS

var pageTemplateFileNames = []string{"page", "header", "footer", "content", "group", "link", "tile"}

var filesystemTemplateFS = managerDirectoryFS()

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

	parsed := template.New("page").Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s) //nolint:gosec
		},
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
	})
	for _, name := range pageTemplateFileNames {
		contents, err := readPageTemplateFile(selectedSet, name)
		if err != nil {
			return nil, err
		}
		target := parsed
		if name != "page" {
			target = parsed.New("_" + name)
		}
		if _, err := target.Parse(string(contents)); err != nil {
			return nil, fmt.Errorf("parse template %q from set %q: %w", name, selectedSet, err)
		}
	}

	return &pageTemplate{tmpl: parsed, set: selectedSet}, nil
}

func readPageTemplateFile(set, name string) ([]byte, error) {
	selectedPath, err := pageTemplatePath(set, name)
	if err != nil {
		return nil, err
	}
	defaultPath, err := pageTemplatePath("default", name)
	if err != nil {
		return nil, err
	}

	candidates := []struct {
		source string
		fsys   fs.FS
		path   string
	}{
		{source: "filesystem", fsys: filesystemTemplateFS, path: selectedPath},
		{source: "embedded selected set", fsys: templateFS, path: selectedPath},
	}
	if selectedPath != defaultPath {
		candidates = append(candidates, struct {
			source string
			fsys   fs.FS
			path   string
		}{source: "embedded default set", fsys: templateFS, path: defaultPath})
	}

	for _, candidate := range candidates {
		contents, err := fs.ReadFile(candidate.fsys, candidate.path)
		if err == nil {
			return contents, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return nil, fmt.Errorf("read %s template %q: %w", candidate.source, candidate.path, err)
	}

	return nil, fmt.Errorf("template %q not found in filesystem set %q, embedded set %q, or embedded default set", name, set, set)
}

func managerDirectoryFS() fs.FS {
	executable, err := os.Executable()
	if err != nil {
		return os.DirFS(".")
	}
	return os.DirFS(filepath.Dir(executable))
}

func pageTemplatePath(set, name string) (string, error) {
	if strings.Contains(set, `\`) {
		return "", fmt.Errorf("template set %q must use slash-separated paths", set)
	}
	for _, part := range strings.Split(set, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("template set %q contains an invalid path segment", set)
		}
	}
	templatePath := path.Join("templates", set, name+".tmpl")
	if !strings.HasPrefix(templatePath, "templates/") {
		return "", fmt.Errorf("template path %q escapes templates directory", templatePath)
	}
	return templatePath, nil
}
