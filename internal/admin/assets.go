package admin

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// staticHandler serves the embedded CSS/JS/font bundle under /static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("admin: static assets: " + err.Error())
	}
	return http.StripPrefix("/static/", http.FileServerFS(sub))
}
