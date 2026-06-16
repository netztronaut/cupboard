package web

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPageTemplateUsesFilesystemOverrideBeforeEmbeddedSet(t *testing.T) {
	dir := t.TempDir()
	useFilesystemTemplates(t, dir)
	writeTemplate(t, filepath.Join(dir, "templates/forecastle/header.tmpl"), `{{define "header"}}<header id="custom-header">{{.Title}}</header>{{end}}`)

	page, err := loadPageTemplate("forecastle")
	if err != nil {
		t.Fatalf("loadPageTemplate() error = %v", err)
	}

	body := executePageTemplate(t, page, pageTemplateData{Title: "Custom", ContentLayout: "grid"})
	if !strings.Contains(body, `<header id="custom-header">Custom</header>`) {
		t.Fatalf("expected filesystem header override: %s", body)
	}
	if strings.Contains(body, `class="fc-header"`) {
		t.Fatalf("expected filesystem header to replace embedded forecastle header: %s", body)
	}
	if !strings.Contains(body, `class="fc-main"`) || !strings.Contains(body, `class="fc-footer"`) {
		t.Fatalf("expected missing files to fall back to embedded forecastle templates: %s", body)
	}
}

func TestLoadPageTemplateFallsBackToEmbeddedDefaultSet(t *testing.T) {
	dir := t.TempDir()
	useFilesystemTemplates(t, dir)
	writeTemplate(t, filepath.Join(dir, "templates/custom/header.tmpl"), `{{define "header"}}<header id="custom-header">{{.Title}}</header>{{end}}`)

	page, err := loadPageTemplate("custom")
	if err != nil {
		t.Fatalf("loadPageTemplate() error = %v", err)
	}

	body := executePageTemplate(t, page, pageTemplateData{Title: "Custom", ContentLayout: "list"})
	if !strings.Contains(body, `<header id="custom-header">Custom</header>`) {
		t.Fatalf("expected filesystem header override: %s", body)
	}
	if !strings.Contains(body, `content--list`) || !strings.Contains(body, `Served by cupboard`) {
		t.Fatalf("expected missing files to fall back to embedded default templates: %s", body)
	}
}

func TestLoadPageTemplateDoesNotLetPartialTextReplacePage(t *testing.T) {
	dir := t.TempDir()
	useFilesystemTemplates(t, dir)
	writeTemplate(t, filepath.Join(dir, "templates/custom/page.tmpl"), `{{define "page"}}<html>{{template "header" .}}</html>{{end}}`)
	writeTemplate(t, filepath.Join(dir, "templates/custom/header.tmpl"), "{{define \"header\"}}<header>Custom</header>{{end}}\nstray text")

	page, err := loadPageTemplate("custom")
	if err != nil {
		t.Fatalf("loadPageTemplate() error = %v", err)
	}

	body := executePageTemplate(t, page, pageTemplateData{Title: "Custom"})
	if body != "<html><header>Custom</header></html>" {
		t.Fatalf("expected page template not to be replaced by partial text, got %q", body)
	}
}

func executePageTemplate(t *testing.T, page *pageTemplate, data pageTemplateData) string {
	t.Helper()
	var body bytes.Buffer
	if err := page.tmpl.ExecuteTemplate(&body, "page", data); err != nil {
		t.Fatalf("ExecuteTemplate() error = %v", err)
	}
	return body.String()
}

func writeTemplate(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func useFilesystemTemplates(t *testing.T, dir string) {
	t.Helper()
	previous := filesystemTemplateFS
	filesystemTemplateFS = os.DirFS(dir)
	t.Cleanup(func() {
		filesystemTemplateFS = previous
	})
}
