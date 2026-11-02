package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler 托管前端静态资源,未命中的路径回退到 SPA index.html
func (s *Server) staticHandler() http.Handler {
	if s.StaticFS == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusNotFound, "前端资源未构建")
		})
	}
	fileServer := http.FileServer(http.FS(s.StaticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeErr(w, http.StatusMethodNotAllowed, "请求方法不支持")
			return
		}
		// 未知 API 路径直接 404 JSON,不回退到 SPA HTML(前端拿到 200 HTML 会报模糊错误)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "接口不存在")
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		st, statErr := fs.Stat(s.StaticFS, p)
		if statErr == nil && st.IsDir() {
			// 目录不列举:否则 /assets/ 之类的路径会返回构建产物清单。
			// 静态资源都是具体文件名,目录请求一律视为不存在。
			// 必须在设置缓存头之前返回,避免把 404 也标成 immutable。
			writeErr(w, http.StatusNotFound, "资源不存在")
			return
		}
		if statErr != nil {
			if p == "version.json" || strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "no-store")
				writeErr(w, http.StatusNotFound, "资源不存在")
				return
			}
			r.URL.Path = "/" // SPA 回退
		}
		// Vite 产物带内容哈希,可长期强缓存;embed.FS 没有 ModTime,不设头会每次全量重下
		if p == "version.json" {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
