package httpapi

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed swagger
var swaggerFS embed.FS

// registerSwagger 挂载离线 Swagger UI(无需外网 CDN):
//   GET /docs          -> 跳转 Swagger UI
//   GET /swagger/*     -> UI 与静态资源(含 openapi.yaml)
//   GET /openapi.yaml  -> 跳转规范文件
func (s *Server) registerSwagger(r chi.Router) {
	sub, err := fs.Sub(swaggerFS, "swagger")
	if err != nil {
		panic(err)
	}
	r.Mount("/swagger", http.StripPrefix("/swagger", http.FileServer(http.FS(sub))))
	r.Get("/docs", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/swagger/", http.StatusFound)
	})
	r.Get("/openapi.yaml", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/swagger/openapi.yaml", http.StatusFound)
	})
}
