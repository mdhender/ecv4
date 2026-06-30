package httputil

import "net/http"

func OpenAPIHandler(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		http.ServeFile(w, r, path)
	}
}
