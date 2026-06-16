package web

import "embed"

//go:embed all:dist all:static
var distFS embed.FS
