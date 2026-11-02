package server

import (
	"fmt"
	"log"
	"net/http"

	"tbmusic/internal/config"
	"tbmusic/internal/store"
)

func (s *Server) handleResizeCache(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		SizeMB int64 `json:"sizeMB"`
	}
	if err := decodeBody(w, r, &req); err != nil || req.SizeMB < 1 || req.SizeMB > config.MaxCacheSizeMB {
		writeErr(w, 400, fmt.Sprintf("缓存大小请输入 1-%d 之间的整数（MB）", config.MaxCacheSizeMB))
		return
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	previous := s.Cache.Stats().Limit
	if err := s.Cache.Resize(req.SizeMB << 20); err != nil {
		log.Printf("[cache] 调整容量失败: %v", err)
		writeErr(w, 500, "调整缓存大小失败，请检查缓存目录权限")
		return
	}
	oldConfig := s.Cfg.GetCacheSizeMB()
	s.Cfg.SetCacheSizeMB(req.SizeMB)
	if err := s.Cfg.Save(s.ConfigPath); err != nil {
		s.Cfg.SetCacheSizeMB(oldConfig)
		if rollbackErr := s.Cache.Resize(previous); rollbackErr != nil {
			log.Printf("[cache] 恢复容量失败: %v", rollbackErr)
		}
		log.Printf("[cache] 保存容量失败: %v", err)
		writeErr(w, 500, "保存缓存大小失败，请检查配置文件权限")
		return
	}
	writeJSON(w, 200, s.Cache.Stats())
}

func (s *Server) handleClearCache(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if err := s.Cache.Clear(); err != nil {
		log.Printf("[cache] 清空失败: %v", err)
		writeErr(w, 500, "部分缓存未能清空，请检查缓存目录权限后重试")
		return
	}
	writeJSON(w, 200, s.Cache.Stats())
}
