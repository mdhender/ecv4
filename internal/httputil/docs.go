package httputil

import (
	"embed"
	"io/fs"
	"net/http"
)

// swaggerUIFiles holds the vendored, pinned Swagger UI distribution (see
// swaggerui/) plus the index.html that points it at /openapi.yaml. Embedding
// keeps the server a single self-contained binary: the docs page renders with
// no network access and no third-party JavaScript fetched at view time.
//
//go:embed swaggerui
var swaggerUIFiles embed.FS

// DocsHandler serves the embedded Swagger UI, rooted at prefix (for example
// "/docs/"). Register it on that same subtree pattern so the relative asset
// requests the page makes (swagger-ui.css, swagger-ui-bundle.js) resolve back
// into the embedded filesystem. It is only wired when the operator opts in with
// --allow-openapi-docs; the caller does not register it otherwise.
func DocsHandler(prefix string) http.Handler {
	// The subdirectory is fixed at build time, so a failure here is a
	// programming error (a renamed or missing embed), not a runtime condition.
	sub, err := fs.Sub(swaggerUIFiles, "swaggerui")
	if err != nil {
		panic("httputil: embedded swaggerui directory missing: " + err.Error())
	}
	return http.StripPrefix(prefix, http.FileServer(http.FS(sub)))
}
