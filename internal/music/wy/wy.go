// Package wy 网易云音乐内置音源:music.163.com 公开接口
package wy

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"tbmusic/internal/music"
)

const base = "https://music.163.com/api"

type Source struct{ music.Base }

var _ music.Source = Source{}

func (Source) ID() string   { return "wy" }
func (Source) Name() string { return "网易" }
func (Source) SearchTypes() []string {
	return []string{"music", "album", "songlist", "artist", "lyric"}
}

// Toplists 网易云官方排行榜列表(详情复用歌单详情接口)
func (s Source) Toplists(ctx context.Context) ([]music.Toplist, error) {
	var out struct {
		List []struct {
			ID    int    `json:"id"`
			Name  string `json:"name"`
			Cover string `json:"coverImgUrl"`
			Freq  string `json:"updateFrequency"`
		} `json:"list"`
	}
	if err := music.GetJSON(ctx, base+"/toplist/detail", wyHeaders(), &out); err != nil {
		return nil, err
	}
	list := make([]music.Toplist, 0, len(out.List))
	for _, t := range out.List {
		if t.ID == 0 || t.Name == "" {
			continue
		}
		list = append(list, music.Toplist{
			ID:   music.Itoa(t.ID),
			Name: t.Name,
			Pic:  strings.ReplaceAll(t.Cover, "http://", "https://"),
			Freq: t.Freq,
		})
	}
	return list, nil
}

func wyHeaders() map[string]string {
	return map[string]string{
		"Referer": "https://music.163.com/",
		"Cookie":  "os=pc; appver=8.10.05; osver=Microsoft-Windows-10",
	}
}

func mstr(m map[string]any, keys ...string) string { return music.Str(m, keys...) }

func joinNames(v any) string {
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	var names []string
	for _, s := range arr {
		if sm, ok := s.(map[string]any); ok {
			if n := mstr(sm, "name"); n != "" {
				names = append(names, n)
			}
		}
	}
	return strings.Join(names, "/")
}

/* ---------- 搜索 ---------- */

type searchResp struct {
	Result struct {
		Songs       []map[string]any `json:"songs"`
		Albums      []map[string]any `json:"albums"`
		Artists     []map[string]any `json:"artists"`
		Playlists   []map[string]any `json:"playlists"`
		SongCount   int              `json:"songCount"`
		AlbumCount  int              `json:"albumCount"`
		ArtistCount int              `json:"artistCount"`
		PlCount     int              `json:"playlistCount"`
	} `json:"result"`
}

func search(ctx context.Context, typeID int, keyword string, page, limit int) (*searchResp, error) {
	u := base + "/search/get/web?s=" + url.QueryEscape(keyword) +
		"&type=" + music.Itoa(typeID) +
		"&offset=" + music.Itoa((page-1)*limit) +
		"&limit=" + music.Itoa(limit) + "&total=true"
	var out searchResp
	if err := music.GetJSON(ctx, u, wyHeaders(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func songFromMap(m map[string]any) *music.Song {
	album := map[string]any{}
	if a, ok := m["album"].(map[string]any); ok {
		album = a
	}
	sec := 0
	if d, ok := m["duration"].(float64); ok {
		sec = int(d) / 1000
	}
	return &music.Song{
		Source:    "wy",
		Songmid:   mstr(m, "id"),
		SongID:    mstr(m, "id"),
		AlbumID:   mstr(album, "id"),
		AlbumName: mstr(album, "name"),
		Name:      mstr(m, "name"),
		Singer:    joinNames(m["artists"]),
		Interval:  music.FormatInterval(sec),
		Pic:       strings.ReplaceAll(mstr(album, "picUrl"), "http://", "https://"),
		Qualities: []string{"128k", "320k"},
	}
}

func (s Source) SearchMusic(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	return s.searchSongs(ctx, 1, keyword, page, limit)
}

func (s Source) SearchLyric(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	return s.searchSongs(ctx, 1006, keyword, page, limit)
}

func (s Source) searchSongs(ctx context.Context, t int, keyword string, page, limit int) (*music.Page[music.Song], error) {
	out, err := search(ctx, t, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	songs := make([]music.Song, 0, len(out.Result.Songs))
	for _, m := range out.Result.Songs {
		if sg := songFromMap(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	// 搜索结果只给 picId,不给封面直链 → 一次性批量补齐,避免前端每首歌再打一次 /api/pic
	fillPics(ctx, songs)
	return &music.Page[music.Song]{
		List: songs, Total: out.Result.SongCount, Page: page, Limit: limit,
		HasMore: music.HasMore(page, limit, out.Result.SongCount, len(songs)),
	}, nil
}

// fillPics 用 song/detail 批量接口补齐封面(一次请求覆盖整页)
func fillPics(ctx context.Context, songs []music.Song) {
	var ids []string
	for i := range songs {
		if songs[i].Pic == "" && isDigits(songs[i].Songmid) {
			ids = append(ids, songs[i].Songmid)
		}
	}
	if len(ids) == 0 {
		return
	}
	if len(ids) > 50 {
		ids = ids[:50]
	}
	var out struct {
		Songs []struct {
			ID    any `json:"id"`
			Album struct {
				PicURL string `json:"picUrl"`
			} `json:"album"`
		} `json:"songs"`
	}
	u := base + "/song/detail?ids=%5B" + strings.Join(ids, "%2C") + "%5D"
	if err := music.GetJSON(ctx, u, wyHeaders(), &out); err != nil {
		return
	}
	pics := make(map[string]string, len(out.Songs))
	for _, s := range out.Songs {
		if s.Album.PicURL == "" {
			continue
		}
		pics[anyID(s.ID)] = strings.ReplaceAll(s.Album.PicURL, "http://", "https://")
	}
	for i := range songs {
		if songs[i].Pic == "" {
			if p, ok := pics[songs[i].Songmid]; ok {
				songs[i].Pic = p
			}
		}
	}
}

func isDigits(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// anyID 把 JSON 数字/字符串形式的 id 统一成十进制字符串
func anyID(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

func (s Source) SearchAlbum(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Album], error) {
	out, err := search(ctx, 10, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	albums := make([]music.Album, 0, len(out.Result.Albums))
	for _, m := range out.Result.Albums {
		albums = append(albums, music.Album{
			ID:     mstr(m, "id"),
			Name:   mstr(m, "name"),
			Singer: joinNames(m["artists"]),
			Pic:    strings.ReplaceAll(mstr(m, "picUrl"), "http://", "https://"),
		})
	}
	return &music.Page[music.Album]{List: albums, Total: out.Result.AlbumCount, Page: page, Limit: limit, HasMore: music.HasMore(page, limit, out.Result.AlbumCount, len(albums))}, nil
}

func (s Source) SearchSonglist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Songlist], error) {
	out, err := search(ctx, 1000, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	lists := make([]music.Songlist, 0, len(out.Result.Playlists))
	for _, m := range out.Result.Playlists {
		creator := map[string]any{}
		if c, ok := m["creator"].(map[string]any); ok {
			creator = c
		}
		cnt := 0
		if n, ok := m["trackCount"].(float64); ok {
			cnt = int(n)
		}
		lists = append(lists, music.Songlist{
			ID:      mstr(m, "id"),
			Name:    mstr(m, "name"),
			Creator: mstr(creator, "nickname"),
			Pic:     strings.ReplaceAll(mstr(m, "coverImgUrl"), "http://", "https://"),
			Count:   cnt,
		})
	}
	return &music.Page[music.Songlist]{List: lists, Total: out.Result.PlCount, Page: page, Limit: limit, HasMore: music.HasMore(page, limit, out.Result.PlCount, len(lists))}, nil
}

func (s Source) SearchArtist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Artist], error) {
	out, err := search(ctx, 100, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	artists := make([]music.Artist, 0, len(out.Result.Artists))
	for _, m := range out.Result.Artists {
		artists = append(artists, music.Artist{
			ID:   mstr(m, "id"),
			Name: mstr(m, "name"),
			Pic:  strings.ReplaceAll(mstr(m, "picUrl"), "http://", "https://"),
		})
	}
	return &music.Page[music.Artist]{List: artists, Total: out.Result.ArtistCount, Page: page, Limit: limit, HasMore: music.HasMore(page, limit, out.Result.ArtistCount, len(artists))}, nil
}

/* ---------- 详情 ---------- */

func (s Source) AlbumSongs(ctx context.Context, albumID string, page, limit int) (*music.Page[music.Song], error) {
	var out struct {
		Album struct {
			Name  string           `json:"name"`
			Songs []map[string]any `json:"songs"`
		} `json:"album"`
	}
	if err := music.GetJSON(ctx, base+"/album/"+url.PathEscape(albumID), wyHeaders(), &out); err != nil {
		return nil, err
	}
	return songPage(out.Album.Songs, page, limit), nil
}

func (s Source) SonglistSongs(ctx context.Context, listID string, page, limit int) (*music.Page[music.Song], error) {
	var out struct {
		Result struct {
			Name   string           `json:"name"`
			Tracks []map[string]any `json:"tracks"`
		} `json:"result"`
	}
	if err := music.GetJSON(ctx, base+"/playlist/detail?id="+url.QueryEscape(listID), wyHeaders(), &out); err != nil {
		return nil, err
	}
	songs := make([]music.Song, 0)
	for _, m := range out.Result.Tracks {
		if sg := songFromMap(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	if len(songs) == 0 {
		return nil, music.ErrUnsupported
	}
	list := music.PageSlice(songs, page, limit)
	return &music.Page[music.Song]{List: list, Total: len(songs), Page: page, Limit: limit, HasMore: page*limit < len(songs)}, nil
}

func (s Source) ArtistSongs(ctx context.Context, artistID string, page, limit int) (*music.Page[music.Song], error) {
	var out struct {
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
		HotSongs []map[string]any `json:"hotSongs"`
	}
	if err := music.GetJSON(ctx, base+"/artist/"+url.PathEscape(artistID), wyHeaders(), &out); err != nil {
		return nil, err
	}
	if len(out.HotSongs) == 0 {
		return nil, music.ErrUnsupported
	}
	return songPage(out.HotSongs, page, limit), nil
}

func songPage(list []map[string]any, page, limit int) *music.Page[music.Song] {
	songs := make([]music.Song, 0, len(list))
	for _, m := range list {
		if sg := songFromMap(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	// 真分页:按 page/limit 切片,避免翻页重复返回全量
	if limit <= 0 {
		limit = 30
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * limit
	if start > len(songs) {
		start = len(songs)
	}
	end := start + limit
	if end > len(songs) {
		end = len(songs)
	}
	return &music.Page[music.Song]{List: songs[start:end], Total: len(songs), Page: page, Limit: limit, HasMore: end < len(songs)}
}

/* ---------- 歌词 / 封面 ---------- */

func (s Source) GetLyric(ctx context.Context, song *music.Song) (*music.Lyrics, error) {
	var out struct {
		Lrc struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLyric struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
	}
	u := base + "/song/lyric?id=" + url.QueryEscape(song.Songmid) + "&lv=1&kv=1&tv=-1"
	if err := music.GetJSON(ctx, u, wyHeaders(), &out); err != nil {
		return nil, err
	}
	if out.Lrc.Lyric == "" {
		return nil, music.ErrUnsupported
	}
	return &music.Lyrics{Lyric: out.Lrc.Lyric, TLyric: out.TLyric.Lyric}, nil
}

func (s Source) GetPic(ctx context.Context, song *music.Song) (string, error) {
	if song.Pic != "" {
		// 请求方可控,校验 scheme 防异常协议(浏览器 CSP 亦有兜底)
		if u, err := url.Parse(song.Pic); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", music.ErrUnsupported
		}
		return song.Pic, nil
	}
	var out struct {
		Songs []struct {
			Album struct {
				PicURL string `json:"picUrl"`
			} `json:"album"`
		} `json:"songs"`
	}
	u := base + "/song/detail?id=" + url.QueryEscape(song.Songmid) + "&ids=%5B" + url.QueryEscape(song.Songmid) + "%5D"
	if err := music.GetJSON(ctx, u, wyHeaders(), &out); err != nil {
		return "", err
	}
	if len(out.Songs) > 0 && out.Songs[0].Album.PicURL != "" {
		return strings.ReplaceAll(out.Songs[0].Album.PicURL, "http://", "https://"), nil
	}
	return "", music.ErrUnsupported
}
