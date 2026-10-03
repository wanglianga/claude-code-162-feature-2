// Package apidoc 通过 go:embed 自托管 OpenAPI 3.x 规范与 Swagger UI(离线可用, 无外部 CDN 依赖)。
package apidoc

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed openapi.yaml
var specFS embed.FS

//go:embed swagger-ui
var uiFS embed.FS

// SpecBytes 返回 OpenAPI 规范原文
func SpecBytes() ([]byte, error) {
	return specFS.ReadFile("openapi.yaml")
}

// Handler 挂载:
//
//	/                 -> Swagger UI (index.html)
//	/openapi.yaml     -> OpenAPI 3.x 规范
//	/swagger-ui/*     -> UI 静态资源
//
// 挂到一个独立 http.ServeMux(如 /docs/) 下使用。
func Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		b, err := SpecBytes()
		if err != nil {
			http.Error(w, "spec not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(b)
	})

	sub, err := fs.Sub(uiFS, "swagger-ui")
	if err != nil {
		panic(err)
	}
	mux.Handle("/swagger-ui/", http.StripPrefix("/swagger-ui/", http.FileServer(http.FS(sub))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	return mux
}

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>区域再生资源回收服务 - API 文档</title>
  <link rel="stylesheet" href="./swagger-ui/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="./swagger-ui/swagger-ui-bundle.js" charset="utf-8"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: './openapi.yaml',
        dom_id: '#swagger-ui',
        deepLinking: true,
        docExpansion: 'list',
        filter: true,
        tryItOutEnabled: true,
        persistAuthorization: true
      });
    };
  </script>
</body>
</html>`
