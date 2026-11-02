package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"tbmusic/internal/music"
	"tbmusic/internal/store"
)

/* ---------- 收藏歌单 ---------- */

// 配额上限:防止注册用户无限堆积数据把 SQLite 撑爆。
const (
	maxPlaylists      = store.MaxPlaylists
	maxPlaylistSongs  = store.MaxPlaylistSongs
	maxBlacklistSongs = store.MaxBlacklistSongs
)

func (s *Server) handleListPlaylists(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.Store.ListPlaylists(u.ID)
	if err != nil {
		log.Printf("[library] 查询歌单失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "查询歌单失败")
		return
	}
	if list == nil {
		list = []store.Playlist{}
	}
	writeJSON(w, 200, map[string]any{"list": list})
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 40 {
		writeErr(w, 400, "歌单名需为 1-40 个字符")
		return
	}
	p, err := s.Store.CreatePlaylist(u.ID, name, false)
	if err != nil {
		if store.IsQuotaExceeded(err) {
			writeErr(w, 400, fmt.Sprintf("歌单数量已达上限(%d 个)", maxPlaylists))
		} else if store.IsDuplicate(err) {
			writeErr(w, 400, "歌单名已存在")
		} else {
			log.Printf("[library] 创建歌单失败 uid=%d: %v", u.ID, err)
			writeErr(w, 500, "创建失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"playlist": p})
}

// pathValueID 解析指定路径变量为正整数
func pathValueID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleRenamePlaylist(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 40 {
		writeErr(w, 400, "歌单名需为 1-40 个字符")
		return
	}
	if err := s.Store.RenamePlaylist(id, u.ID, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "歌单不存在")
		} else if errors.Is(err, store.ErrDefaultPlaylist) {
			writeErr(w, 400, "默认歌单不可重命名")
		} else if store.IsDuplicate(err) {
			writeErr(w, 400, "歌单名已存在")
		} else {
			log.Printf("[library] 重命名歌单失败 uid=%d pid=%d: %v", u.ID, id, err)
			writeErr(w, 500, "重命名失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	if err := s.Store.DeletePlaylist(id, u.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, 404, "歌单不存在")
		case errors.Is(err, store.ErrDefaultPlaylist):
			writeErr(w, 400, err.Error())
		default:
			log.Printf("[library] 删除歌单失败 uid=%d pid=%d: %v", u.ID, id, err)
			writeErr(w, 500, "删除失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handlePlaylistSongs(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	before, limit, err := libraryPage(r)
	if err != nil {
		writeErr(w, 400, "分页参数非法")
		return
	}
	list, err := s.Store.PlaylistSongsPage(id, u.ID, r.URL.Query().Get("q"), before, limit+1)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "歌单不存在")
		} else {
			log.Printf("[library] 查询歌单歌曲失败 uid=%d pid=%d: %v", u.ID, id, err)
			writeErr(w, 500, "查询失败,请稍后重试")
		}
		return
	}
	writeSongPage(w, list, limit)
}

// clipStr 按 rune 截断字符串,防超大字段入库后查询时撑爆内存
func clipStr(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return string(r[:n])
}

// decodeSong 解析并裁剪歌曲信息;字段截断防超大字段入库后批量查询爆内存
func (s *Server) decodeSong(w http.ResponseWriter, r *http.Request) (*music.Song, error) {
	var req struct {
		MusicInfo music.Song `json:"musicInfo"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return nil, err
	}
	g := &req.MusicInfo
	if g.Source == "" || g.Name == "" {
		return nil, errBadSong
	}
	// 音源必须是已注册的内置音源:否则脏数据入库后取链必失败、
	// 且缓存键/黑名单(按 source+song_id 匹配)都会被污染
	if _, ok := s.source(g.Source); !ok {
		return nil, errBadSource
	}
	g.Source = clipStr(g.Source, 16)
	g.Name = clipStr(g.Name, 200)
	g.Singer = clipStr(g.Singer, 200)
	g.AlbumName = clipStr(g.AlbumName, 200)
	g.AlbumID = clipStr(g.AlbumID, 128)
	g.AlbumMid = clipStr(g.AlbumMid, 128)
	g.Songmid = clipStr(g.Songmid, 128)
	g.SongID = clipStr(g.SongID, 128)
	g.Hash = clipStr(g.Hash, 128)
	g.CopyrightID = clipStr(g.CopyrightID, 128)
	g.Interval = clipStr(g.Interval, 16)
	g.Pic = clipStr(g.Pic, 512)
	g.LrcURL = clipStr(g.LrcURL, 512)
	// Qualities 是数组,若不截断可塞满整个 1MB 请求体
	if len(g.Qualities) > 8 {
		g.Qualities = g.Qualities[:8]
	}
	for i, q := range g.Qualities {
		g.Qualities[i] = clipStr(q, 16)
	}
	if g.Key() == g.Source+":" {
		return nil, errBadSong // 没有任何可用 ID,收藏/历史上会互相覆盖
	}
	return g, nil
}

var errBadSong = &staticErr{"歌曲信息不完整"}

var errBadSource = &staticErr{"未知音源"}

type staticErr struct{ msg string }

func (e *staticErr) Error() string { return e.msg }

func (s *Server) handleAddPlaylistSong(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	song, err := s.decodeSong(w, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	added, rowID, err := s.Store.AddSongToPlaylist(id, u.ID, song)
	if err != nil {
		if store.IsQuotaExceeded(err) {
			writeErr(w, 400, fmt.Sprintf("该歌单已达上限(%d 首)", maxPlaylistSongs))
		} else if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "歌单不存在")
		} else {
			log.Printf("[library] 收藏失败 uid=%d pid=%d: %v", u.ID, id, err)
			writeErr(w, 500, "收藏失败,请稍后重试")
		}
		return
	}
	// id 供前端取消收藏时直接 DELETE,省掉一次整表查询
	writeJSON(w, 200, map[string]any{"added": added, "id": rowID})
}

func (s *Server) handleRemovePlaylistSong(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	sid, ok := pathValueID(r, "sid")
	if !ok {
		writeErr(w, 400, "sid 非法")
		return
	}
	if err := s.Store.RemovePlaylistSong(sid, id, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "歌曲不存在或已被移除")
		} else {
			log.Printf("[library] 移除歌曲失败 uid=%d pid=%d sid=%d: %v", u.ID, id, sid, err)
			writeErr(w, 500, "移除失败,请稍后重试")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

/* ---------- 拉黑 ---------- */

func (s *Server) handleListBlacklist(w http.ResponseWriter, r *http.Request, u *store.User) {
	before, limit, err := libraryPage(r)
	if err != nil {
		writeErr(w, 400, "分页参数非法")
		return
	}
	list, err := s.Store.BlacklistPage(u.ID, r.URL.Query().Get("q"), before, limit+1)
	if err != nil {
		log.Printf("[library] 查询黑名单失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "查询失败,请稍后重试")
		return
	}
	writeSongPage(w, list, limit)
}

func (s *Server) handleAddBlacklist(w http.ResponseWriter, r *http.Request, u *store.User) {
	song, err := s.decodeSong(w, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// 拉黑联动:同一事务内加入黑名单并把歌曲移出所有收藏歌单
	added, rowID, createdAt, err := s.Store.AddBlacklistAndClean(u.ID, song)
	if err != nil {
		if store.IsQuotaExceeded(err) {
			writeErr(w, 400, fmt.Sprintf("黑名单已达上限(%d 首)", maxBlacklistSongs))
			return
		}
		log.Printf("[library] 拉黑失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "拉黑失败,请稍后重试")
		return
	}
	// id/createdAt 供前端本地插入黑名单条目,省掉一次全量刷新
	writeJSON(w, 200, map[string]any{
		"added": added, "removedFromPlaylists": true,
		"id": rowID, "createdAt": createdAt,
	})
}

func (s *Server) handleRemoveBlacklist(w http.ResponseWriter, r *http.Request, u *store.User) {
	id, ok := pathValueID(r, "id")
	if !ok {
		writeErr(w, 400, "id 非法")
		return
	}
	if err := s.Store.RemoveBlacklist(id, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, "记录不存在或已被移除")
			return
		}
		log.Printf("[library] 移除黑名单失败 uid=%d id=%d: %v", u.ID, id, err)
		writeErr(w, 500, "移除失败,请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

/* ---------- 播放历史 ---------- */

func (s *Server) handleListHistory(w http.ResponseWriter, r *http.Request, u *store.User) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	if limit > 500 {
		limit = 500
	}
	list, err := s.Store.ListHistory(u.ID, r.URL.Query().Get("s"), limit)
	if err != nil {
		log.Printf("[library] 查询历史失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "查询失败,请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]any{"list": list})
}

func (s *Server) handleAddHistory(w http.ResponseWriter, r *http.Request, u *store.User) {
	song, err := s.decodeSong(w, r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	count, err := s.Store.RecordPlay(u.ID, song)
	if err != nil {
		log.Printf("[library] 写入历史失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "记录失败,请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "listenCount": count})
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request, u *store.User) {
	if err := s.Store.ClearHistory(u.ID); err != nil {
		log.Printf("[library] 清空历史失败 uid=%d: %v", u.ID, err)
		writeErr(w, 500, "清空失败,请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func libraryPage(r *http.Request) (int64, int, error) {
	var before int64
	limit := 200
	var err error
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			return 0, 0, errBadSong
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			return 0, 0, errBadSong
		}
	}
	return before, limit, nil
}

func writeSongPage(w http.ResponseWriter, list []store.SongEntry, limit int) {
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	if list == nil {
		list = []store.SongEntry{}
	}
	var cursor int64
	if more {
		cursor = list[len(list)-1].ID
	}
	writeJSON(w, 200, map[string]any{"list": list, "hasMore": more, "nextCursor": cursor})
}
