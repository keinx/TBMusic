// Package tx QQ 音乐内置音源:搜索/详情(wapi u.y.qq.com musicu + v8 fcg + SSR 页面)、
// 歌词(c.y.qq.com fcg,base64)
package tx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"tbmusic/internal/music"
)

const musicuURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"

type Source struct{ music.Base }

var _ music.Source = Source{}

func (Source) ID() string   { return "tx" }
func (Source) Name() string { return "QQ" }
func (Source) SearchTypes() []string {
	return []string{"music", "album", "songlist", "artist", "lyric"}
}

/* ---------- musicu 调用 ---------- */

type musicuResp struct {
	Req1 struct {
		Code int `json:"code"`
		Data struct {
			Body struct {
				Song struct {
					Curnum int              `json:"curnum"`
					Sum    int              `json:"sum"`
					List   []map[string]any `json:"list"`
				} `json:"song"`
				Singer struct {
					List []map[string]any `json:"list"`
				} `json:"singer"`
				Album struct {
					List []map[string]any `json:"list"`
				} `json:"album"`
				Songlist struct {
					List []map[string]any `json:"list"`
				} `json:"songlist"`
			} `json:"body"`
		} `json:"data"`
	} `json:"req_1"`
}

func musicuSearch(ctx context.Context, searchType int, keyword string, page, limit int) (*musicuResp, error) {
	payload := map[string]any{
		"req_1": map[string]any{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicDesktop",
			"param": map[string]any{
				"page":        page,
				"search_type": searchType,
				"query":       keyword,
				"grp":         1,
			},
		},
	}
	var out musicuResp
	if err := music.PostJSON(ctx, musicuURL, map[string]string{"Referer": "https://y.qq.com/"}, payload, &out); err != nil {
		return nil, err
	}
	if out.Req1.Code != 0 {
		return nil, music.ErrUnsupported
	}
	return &out, nil
}

func mstr(m map[string]any, keys ...string) string { return music.Str(m, keys...) }
func mint(m map[string]any, keys ...string) int    { return music.Int(m, keys...) }

// txPageSize QQ 音乐搜索接口固定每页 15 条(且已不再返回总数 sum 字段),
// 因此分页判定只能按"本页是否满 15 条"来做。
const txPageSize = 15

func singerJoin(v any) string {
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

func subMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

// txSongFromItem 把 musicu/SSR 的歌曲对象转成通用 Song
func txSongFromItem(m map[string]any) *music.Song {
	album := subMap(m, "album")
	file := subMap(m, "file")
	qualities := []string{"128k"}
	if mint(file, "size_320mp3") > 0 {
		qualities = append(qualities, "320k")
	}
	if mint(file, "size_flac") > 0 {
		qualities = append(qualities, "flac")
	}
	if mint(file, "size_hires") > 0 {
		qualities = append(qualities, "flac24bit")
	}
	albumMid := mstr(album, "mid", "albummid")
	return &music.Song{
		Source:    "tx",
		Songmid:   mstr(m, "mid", "songmid"),
		SongID:    mstr(m, "id", "songid"),
		AlbumID:   mstr(album, "id", "albumid"),
		AlbumMid:  albumMid,
		AlbumName: mstr(album, "name", "albumname"),
		Name:      mstr(m, "name", "songname", "title"),
		Singer:    singerJoin(m["singer"]),
		Interval:  music.FormatInterval(mint(m, "interval", "duration")),
		Pic:       albumPic(albumMid), // 搜索结果直接带封面,前端无需再补请求
		Qualities: qualities,
	}
}

/* ---------- 搜索 ---------- */

func (s Source) SearchMusic(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	return s.searchSongs(ctx, 0, keyword, page, limit)
}

func (s Source) SearchLyric(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	return s.searchSongs(ctx, 7, keyword, page, limit)
}

func (s Source) searchSongs(ctx context.Context, t int, keyword string, page, limit int) (*music.Page[music.Song], error) {
	out, err := musicuSearch(ctx, t, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	list := out.Req1.Data.Body.Song.List
	songs := make([]music.Song, 0, len(list))
	for _, m := range list {
		if sg := txSongFromItem(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	return &music.Page[music.Song]{
		List: songs, Total: 0, Page: page, Limit: limit,
		HasMore: len(list) >= txPageSize,
	}, nil
}

func (s Source) SearchAlbum(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Album], error) {
	out, err := musicuSearch(ctx, 2, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	list := out.Req1.Data.Body.Album.List
	albums := make([]music.Album, 0, len(list))
	for _, m := range list {
		id := mstr(m, "albumID", "albumid")
		mid := mstr(m, "albumMID", "albummid")
		if id == "" && mid == "" {
			continue
		}
		albums = append(albums, music.Album{
			ID:      id + "|" + mid,
			Name:    mstr(m, "albumName"),
			Singer:  mstr(m, "singerName"),
			Pic:     albumPic(mid),
			Publish: mstr(m, "publicTime"),
		})
	}
	return &music.Page[music.Album]{List: albums, Total: len(albums), Page: page, Limit: limit, HasMore: len(list) >= txPageSize}, nil
}

func (s Source) SearchSonglist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Songlist], error) {
	out, err := musicuSearch(ctx, 3, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	list := out.Req1.Data.Body.Songlist.List
	playlists := make([]music.Songlist, 0, len(list))
	for _, m := range list {
		id := mstr(m, "dissid")
		if id == "" {
			continue
		}
		creator := subMap(m, "creator")
		playlists = append(playlists, music.Songlist{
			ID:      id,
			Name:    mstr(m, "dissname"),
			Creator: mstr(creator, "name"),
			Pic:     strings.ReplaceAll(mstr(m, "imgurl"), "http://", "https://"),
		})
	}
	return &music.Page[music.Songlist]{List: playlists, Total: len(playlists), Page: page, Limit: limit, HasMore: len(list) >= txPageSize}, nil
}

func (s Source) SearchArtist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Artist], error) {
	out, err := musicuSearch(ctx, 1, keyword, page, limit)
	if err != nil {
		return nil, err
	}
	list := out.Req1.Data.Body.Singer.List
	artists := make([]music.Artist, 0, len(list))
	for _, m := range list {
		// 歌手搜索返回的字段是 singerMID/singerName/singerPic(与歌曲里的 mid/name 不同)
		id := mstr(m, "singerMID", "mid")
		if id == "" {
			continue
		}
		artists = append(artists, music.Artist{
			ID:   id,
			Name: mstr(m, "singerName", "name"),
			Pic:  strings.ReplaceAll(mstr(m, "singerPic", "pic"), "http://", "https://"),
		})
	}
	return &music.Page[music.Artist]{List: artists, Total: len(artists), Page: page, Limit: limit, HasMore: len(list) >= txPageSize}, nil
}

func albumPic(albumMid string) string {
	if albumMid == "" {
		return ""
	}
	// 尺寸必须是 300/500 这类受支持的规格:480x480 在 gtimg 上直接 404
	// (实测多个专辑:480 全 404,300 与 500 均 200),这是此前 QQ 封面空白的根因
	return "https://y.gtimg.cn/music/photo_new/T002R500x500M000" + albumMid + ".jpg"
}

/* ---------- 详情 ---------- */

// AlbumSongs 专辑详情(v8 fcg);id 形如 "8220|000MkMni19ClKG"
func (s Source) AlbumSongs(ctx context.Context, id string, page, limit int) (*music.Page[music.Song], error) {
	mid := id
	if i := strings.Index(id, "|"); i >= 0 {
		mid = id[i+1:]
	}
	var out struct {
		Data struct {
			Name  string           `json:"name"`
			Total int              `json:"total"`
			List  []map[string]any `json:"list"`
		} `json:"data"`
	}
	u := "https://c.y.qq.com/v8/fcg-bin/fcg_v8_album_info_cp.fcg?albummid=" + url.QueryEscape(mid) + "&format=json&inCharset=utf8&outCharset=utf-8"
	if err := music.GetJSON(ctx, u, map[string]string{"Referer": "https://y.qq.com/"}, &out); err != nil {
		return nil, err
	}
	songs := make([]music.Song, 0, len(out.Data.List))
	for _, m := range out.Data.List {
		if sg := txSongFromItem(m); sg.Songmid != "" {
			sg.AlbumMid = mid
			songs = append(songs, *sg)
		}
	}
	list := music.PageSlice(songs, page, limit)
	return &music.Page[music.Song]{List: list, Total: out.Data.Total, Page: page, Limit: limit, HasMore: page*limit < len(songs)}, nil
}

// extractInitJSON 从 SSR 页面提取 window.__INITIAL_DATA__ 的完整 JSON
func extractInitJSON(html string) string {
	marker := strings.Index(html, "__INITIAL_DATA__")
	if marker < 0 {
		return ""
	}
	start := strings.Index(html[marker:], "{")
	if start < 0 {
		return ""
	}
	i := marker + start
	depth := 0
	inStr := false
	esc := false
	for j := i; j < len(html); j++ {
		c := html[j]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.ReplaceAll(html[i:j+1], ":undefined", ":null")
			}
		}
	}
	return ""
}

func songsFromSSR(html string) []music.Song {
	raw := extractInitJSON(html)
	if raw == "" {
		return nil
	}
	var out map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	var list []map[string]any
	if d, ok := out["detail"]; ok { // 歌单页
		var dm map[string]json.RawMessage
		json.Unmarshal(d, &dm)
		json.Unmarshal(dm["songList"], &list)
	} else { // 歌手页
		json.Unmarshal(out["songList"], &list)
	}
	songs := make([]music.Song, 0, len(list))
	for _, m := range list {
		if si, ok := m["songInfo"].(map[string]any); ok {
			m = si
		}
		if sg := txSongFromItem(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	return songs
}

// ssrCache 歌单/歌手详情页是整页 SSR HTML(单次数百 KB),同一 id 翻页会重复抓取,
// 这里按 URL 缓存 5 分钟。切片只读共享,调用方不得修改元素。
var ssrCache = struct {
	mu sync.Mutex
	m  map[string]ssrEntry
}{m: make(map[string]ssrEntry)}

type ssrEntry struct {
	songs []music.Song
	exp   time.Time
}

const ssrCacheTTL = 5 * time.Minute

// ssrCacheMax 缓存条目硬上限(超出后先清过期项,仍超则随机淘汰)
const ssrCacheMax = 256

func fetchSSRSongs(ctx context.Context, pageURL string) ([]music.Song, error) {
	now := time.Now()
	ssrCache.mu.Lock()
	if e, ok := ssrCache.m[pageURL]; ok && now.Before(e.exp) {
		ssrCache.mu.Unlock()
		return e.songs, nil
	}
	ssrCache.mu.Unlock()

	_, body, err := music.GetString(ctx, pageURL, map[string]string{
		"User-Agent": music.DefaultUA, "Referer": "https://y.qq.com/",
	})
	if err != nil {
		return nil, err
	}
	songs := songsFromSSR(body)
	if len(songs) == 0 {
		return songs, nil
	}
	ssrCache.mu.Lock()
	// key 由请求方传入的 id 决定,必须硬性限制条数:仅清理过期项时,
	// 持续用不同 id 请求会让 map 无界增长(每条还挂着上百首歌的结构体)。
	if len(ssrCache.m) >= ssrCacheMax {
		for k, v := range ssrCache.m {
			if now.After(v.exp) {
				delete(ssrCache.m, k)
			}
		}
		if over := len(ssrCache.m) - ssrCacheMax*9/10; over > 0 {
			for k := range ssrCache.m {
				if over <= 0 {
					break
				}
				delete(ssrCache.m, k)
				over--
			}
		}
	}
	ssrCache.m[pageURL] = ssrEntry{songs: songs, exp: now.Add(ssrCacheTTL)}
	ssrCache.mu.Unlock()
	return songs, nil
}

func (s Source) SonglistSongs(ctx context.Context, id string, page, limit int) (*music.Page[music.Song], error) {
	songs, err := fetchSSRSongs(ctx, "https://y.qq.com/n/ryqq/playlist/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	if len(songs) == 0 {
		return nil, music.ErrUnsupported
	}
	list := music.PageSlice(songs, page, limit)
	return &music.Page[music.Song]{List: list, Total: len(songs), Page: page, Limit: limit, HasMore: page*limit < len(songs)}, nil
}

func (s Source) ArtistSongs(ctx context.Context, id string, page, limit int) (*music.Page[music.Song], error) {
	songs, err := fetchSSRSongs(ctx, "https://y.qq.com/n/ryqq/singer/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	if len(songs) == 0 {
		return nil, music.ErrUnsupported
	}
	list := music.PageSlice(songs, page, limit)
	return &music.Page[music.Song]{List: list, Total: len(songs), Page: page, Limit: limit, HasMore: page*limit < len(songs)}, nil
}

/* ---------- 歌词 / 封面 ---------- */

func (s Source) GetLyric(ctx context.Context, song *music.Song) (*music.Lyrics, error) {
	var out struct {
		Retcode int    `json:"retcode"`
		Lyric   string `json:"lyric"`
		Trans   string `json:"trans"`
	}
	u := "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?songmid=" + url.QueryEscape(song.Songmid) +
		"&g_tk=5381&loginUin=0&hostUin=0&format=json&inCharset=auto&outCharset=utf-8&notice=0&platform=yqq&needNewCode=0"
	if err := music.GetJSON(ctx, u, map[string]string{"Referer": "https://music.qq.com/"}, &out); err != nil {
		return nil, err
	}
	decode := func(s string) string {
		if s == "" {
			return ""
		}
		if b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", "")); err == nil {
			return string(b)
		}
		return s
	}
	lrc := decode(out.Lyric)
	if lrc == "" {
		return nil, music.ErrUnsupported
	}
	return &music.Lyrics{Lyric: lrc, TLyric: decode(out.Trans)}, nil
}

func (s Source) GetPic(ctx context.Context, song *music.Song) (string, error) {
	if song.AlbumMid != "" {
		return albumPic(song.AlbumMid), nil
	}
	return "", music.ErrUnsupported
}
