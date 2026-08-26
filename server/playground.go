package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// playgroundAssets are deliberately outside the OGC API contract. They are a
// read-only browser for developers and operators, embedded so a Tlon deployment
// does not need a second web server.
//
//go:embed playground/*
var playgroundAssets embed.FS

func playgroundHandler() http.Handler {
	root, err := fs.Sub(playgroundAssets, "playground")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/playground")
		index := path == "" || path == "/"
		if index {
			path = "/"
		}
		request := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = path
		request.URL = &urlCopy
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if index {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		files.ServeHTTP(w, request)
	})
}
