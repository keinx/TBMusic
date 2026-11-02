// Package kw 酷我内置音源:搜索(search.kuwo.cn r.s 老接口)、
// 详情/封面(wapi.kuwo.cn)、歌词(www.kuwo.cn openapi)
package kw

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	mrand "math/rand"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"tbmusic/internal/music"
)

const (
	rsURL      = "https://search.kuwo.cn/r.s"
	wapiBase   = "https://wapi.kuwo.cn/api/www"
	lyricURL   = "https://www.kuwo.cn/openapi/v1/www/lyric/getlyric"
	randomSeed = "热门 经典 抖音 华语 流行 伤感 轻音乐 民谣 摇滚 粤语 治愈 夜听 翻唱 纯音乐 现场 神曲 回忆 恋爱 励志"
)

type Source struct{ music.Base }

var _ music.Source = Source{}

func (Source) ID() string   { return "kw" }
func (Source) Name() string { return "酷我" }
func (Source) SearchTypes() []string {
	return []string{"music", "album", "songlist", "artist"}
}

func kwHeaders() map[string]string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		// 随机源异常时退化为时间戳,保证 token 仍唯一
		binary.LittleEndian.PutUint64(b, uint64(time.Now().UnixNano()))
	}
	token := hex.EncodeToString(b)
	return map[string]string{
		"csrf":    token,
		"Cookie":  "kw_token=" + token,
		"Referer": "http://www.kuwo.cn/",
	}
}

/* ---------- r.s 伪 JSON 解析 ---------- */

// fixKWJSON 把酷我 r.s 返回的 Python 风格伪 JSON 转为标准 JSON
func fixKWJSON(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&quot;", `\"`)
	s = strings.ReplaceAll(s, "&amp;", "&")
	var b strings.Builder
	b.Grow(len(s) + 256)
	inStr := false
	lastSignificant := byte('{')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\'':
				inStr = false
				b.WriteByte('"')
			case '\\':
				b.WriteByte(c)
				if i+1 < len(s) {
					i++
					b.WriteByte(s[i])
				}
			case '"':
				b.WriteString(`\"`)
			default:
				b.WriteByte(c)
			}
			continue
		}
		switch {
		case c == '\'':
			inStr = true
			b.WriteByte('"')
		case c == '{' || c == ',':
			b.WriteByte(c)
			lastSignificant = c
		case isKeyStart(c) && (lastSignificant == '{' || lastSignificant == ','):
			// 键名:一直读到 ':',加引号
			j := i
			for j < len(s) && isKeyChar(s[j]) {
				j++
			}
			if j < len(s) && s[j] == ':' {
				b.WriteByte('"')
				b.WriteString(s[i:j])
				b.WriteString(`":`)
				i = j
				lastSignificant = ':'
			} else {
				b.WriteByte(c)
			}
		default:
			if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				lastSignificant = c
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isKeyStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isKeyChar(c byte) bool {
	return isKeyStart(c) || c == '.' || (c >= '0' && c <= '9')
}

type rsPage struct {
	Total     string          `json:"TOTAL"`
	Abslist   json.RawMessage `json:"abslist"`
	AlbumList json.RawMessage `json:"albumlist"`
}

func rsQuery(ctx context.Context, keyword, ft string, page, limit int) (*rsPage, error) {
	v := url.Values{}
	v.Set("all", keyword)
	v.Set("ft", ft)
	v.Set("itemset", "web_2013")
	v.Set("client", "kt")
	v.Set("pn", strconv.Itoa(page-1))
	v.Set("rn", strconv.Itoa(limit))
	v.Set("rformat", "json")
	v.Set("encoding", "utf8")
	_, body, err := music.GetString(ctx, rsURL+"?"+v.Encode(), map[string]string{"User-Agent": music.DefaultUA})
	if err != nil {
		return nil, err
	}
	var p rsPage
	if err := json.Unmarshal([]byte(fixKWJSON(body)), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// listFromRaw 解析 abslist/albumlist 为通用 map 列表
func listFromRaw(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var items []map[string]any
	json.Unmarshal(raw, &items)
	return items
}

/* ---------- 通用字段提取 ---------- */

// mstr 取出字段并还原酷我的双重转义(r.s 接口的老毛病)
func mstr(m map[string]any, keys ...string) string { return unescapeKW(music.Str(m, keys...)) }
func mint(m map[string]any, keys ...string) int    { return music.Int(m, keys...) }

// unescapeKW 还原酷我 r.s 接口额外的字面量转义。
// 上游把 "&" 写成多个反斜杠 + "u0026"(而不是 JSON 规范里的单个反斜杠),
// 每多一层 JSON 编码就多一层反斜杠,经 fixKWJSON 与 json.Unmarshal 后仍会残留
// "\u0026" 甚至 "\\\u0026",最终在页面上显示成乱码,例如 "毛不易\u0026段奥娟"。
// 这里把「若干反斜杠 + uXXXX」整体还原成真实字符。
func unescapeKW(s string) string {
	if !strings.Contains(s, `\u`) && !strings.Contains(s, `\\u`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			// 数一下连续反斜杠的个数
			n := 0
			for i+n < len(s) && s[i+n] == '\\' {
				n++
			}
			if i+n+5 <= len(s) && s[i+n] == 'u' {
				if v, err := strconv.ParseUint(s[i+n+1:i+n+5], 16, 32); err == nil {
					r := rune(v)
					i += n + 5
					// 代理对(emoji 等)需要拼上后半个码点
					if r >= 0xD800 && r <= 0xDBFF {
						m := 0
						for i+m < len(s) && s[i+m] == '\\' {
							m++
						}
						if i+m+5 <= len(s) && s[i+m] == 'u' {
							if v2, err2 := strconv.ParseUint(s[i+m+1:i+m+5], 16, 32); err2 == nil {
								if r2 := rune(v2); r2 >= 0xDC00 && r2 <= 0xDFFF {
									r = (r-0xD800)<<10 + (r2 - 0xDC00) + 0x10000
									i += m + 5
								}
							}
						}
					}
					b.WriteRune(r)
					continue
				}
			}
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// albumIDOf 酷我搜索接口在没有专辑信息时会返回字符串 "0",按空值处理
func albumIDOf(m map[string]any) string {
	id := mstr(m, "ALBUMID", "albumid", "albumId")
	if id == "0" {
		return ""
	}
	return id
}

func ridOf(m map[string]any) string {
	rid := mstr(m, "MUSICRID", "musicrid", "rid", "DC_TARGETID")
	return strings.TrimPrefix(rid, "MUSIC_")
}

func singerJoin(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var names []string
		for _, s := range x {
			if sm, ok := s.(map[string]any); ok {
				if n := mstr(sm, "name"); n != "" {
					names = append(names, n)
				}
			}
		}
		return strings.Join(names, "/")
	}
	return ""
}

// qualitiesFromMinfo 从 "level:p,bitrate:320,format:mp3;..." 提取音质标签。
// 酷我的无损在各接口里叫法不一(flac / mflac / zpga / zply),需要一起识别,
// 否则大量 FLAC 曲目会被误标为 128k,导致取链降级链少试音质。
func qualitiesFromMinfo(minfo string) []string {
	qs := []string{"128k"}
	if strings.Contains(minfo, "bitrate:320") {
		qs = append(qs, "320k")
	}
	if strings.Contains(minfo, "format:flac") || strings.Contains(minfo, "format:mflac") ||
		strings.Contains(minfo, "format:zp") || strings.Contains(minfo, "level:zp") {
		qs = append(qs, "flac")
	}
	return qs
}

func songFromMap(m map[string]any) *music.Song {
	sec := mint(m, "DURATION", "duration", "Duration")
	if ms := mint(m, "songTimeMsecs", "durationMs"); ms > sec*1000+1 { // www 接口毫秒
		sec = ms / 1000
	}
	qualities := qualitiesFromMinfo(mstr(m, "N_MINFO", "MINFO"))
	return &music.Song{
		Source:    "kw",
		Songmid:   ridOf(m),
		SongID:    ridOf(m),
		AlbumID:   albumIDOf(m),
		AlbumName: mstr(m, "ALBUM", "album"),
		Name:      mstr(m, "SONGNAME", "name"),
		Singer:    mstr(m, "ARTIST", "artist"),
		Interval:  music.FormatInterval(sec),
		Pic:       upgradePic(mstr(m, "pic", "albumpic", "PICPATH")),
		Qualities: qualities,
	}
}

func upgradePic(pic string) string {
	if pic == "" {
		return ""
	}
	pic = strings.ReplaceAll(pic, "/120/", "/480/")
	pic = strings.ReplaceAll(pic, "/240/", "/480/")
	pic = strings.ReplaceAll(pic, "/300/", "/480/")
	return pic
}

// trimPicSize 去掉酷我头像相对路径开头的尺寸段("240/xxx.jpg" → "xxx.jpg"),
// 尺寸统一由 starheads/<尺寸>/ 前缀决定。
func trimPicSize(p string) string {
	p = strings.TrimPrefix(p, "/")
	if len(p) > 3 && p[3] == '/' &&
		p[0] >= '0' && p[0] <= '9' && p[1] >= '0' && p[1] <= '9' && p[2] >= '0' && p[2] <= '9' {
		return p[4:]
	}
	return p
}

/* ---------- 搜索 ---------- */

func (s Source) SearchMusic(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	p, err := rsQuery(ctx, keyword, "music", page, limit)
	if err != nil {
		return nil, err
	}
	list := listFromRaw(p.Abslist)
	songs := make([]music.Song, 0, len(list))
	for _, m := range list {
		if sg := songFromMap(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	return pageOf(songs, p.Total, page, limit), nil
}

func (s Source) SearchAlbum(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Album], error) {
	p, err := rsQuery(ctx, keyword, "album", page, limit)
	if err != nil {
		return nil, err
	}
	list := listFromRaw(p.AlbumList)
	if list == nil {
		list = listFromRaw(p.Abslist)
	}
	albums := make([]music.Album, 0, len(list))
	for _, m := range list {
		id := mstr(m, "albumid", "id", "DC_TARGETID")
		if id == "" {
			continue
		}
		albums = append(albums, music.Album{
			ID:      id,
			Name:    mstr(m, "album", "name"),
			Singer:  mstr(m, "artist"),
			Pic:     upgradePic(mstr(m, "hts_img", "img")),
			Publish: mstr(m, "publictime", "publish"),
		})
	}
	return pageOf(albums, p.Total, page, limit), nil
}

func (s Source) SearchSonglist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Songlist], error) {
	p, err := rsQuery(ctx, keyword, "playlist", page, limit)
	if err != nil {
		return nil, err
	}
	list := listFromRaw(p.Abslist)
	playlists := make([]music.Songlist, 0, len(list))
	for _, m := range list {
		id := mstr(m, "DC_TARGETID", "id")
		if id == "" {
			continue
		}
		playlists = append(playlists, music.Songlist{
			ID:      id,
			Name:    mstr(m, "name", "title"),
			Creator: mstr(m, "nickname", "uname"),
			Pic:     upgradePic(mstr(m, "hts_pic", "img")),
			Count:   mint(m, "total", "songnum"),
		})
	}
	return pageOf(playlists, p.Total, page, limit), nil
}

func (s Source) SearchArtist(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Artist], error) {
	p, err := rsQuery(ctx, keyword, "artist", page, limit)
	if err != nil {
		return nil, err
	}
	list := listFromRaw(p.Abslist)
	artists := make([]music.Artist, 0, len(list))
	for _, m := range list {
		id := mstr(m, "ARTISTID", "DC_TARGETID", "artistid")
		if id == "" {
			continue
		}
		pic := mstr(m, "PICPATH")
		if pic != "" && !strings.HasPrefix(pic, "http") {
			// 酷我返回的是 "240/s4s56/58/291211030.jpg" 这类相对路径,
			// 尺寸段由前面的 starheads/<尺寸>/ 指定,重复拼接会 404
			pic = "https://img1.kuwo.cn/star/starheads/400/" + trimPicSize(pic)
		}
		// 页面按 HTTPS 提供时必须用 https,否则会被 CSP/mixed-content 拦掉
		pic = strings.ReplaceAll(pic, "http://", "https://")
		artists = append(artists, music.Artist{
			ID:   id,
			Name: mstr(m, "ARTIST", "name"),
			Pic:  pic,
		})
	}
	return pageOf(artists, p.Total, page, limit), nil
}

func pageOf[T any](list []T, total string, page, limit int) *music.Page[T] {
	n := 0
	if t, err := strconv.Atoi(total); err == nil {
		n = t
	}
	return &music.Page[T]{
		List: list, Total: n, Page: page, Limit: limit,
		HasMore: music.HasMore(page, limit, n, len(list)),
	}
}

/* ---------- 详情 ---------- */

func wwwList(ctx context.Context, path string, page, limit int, keys ...string) ([]map[string]any, error) {
	v := url.Values{}
	v.Set("pn", strconv.Itoa(page))
	v.Set("rn", strconv.Itoa(limit))
	v.Set("httpsStatus", "1")
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var out map[string]json.RawMessage
	if err := music.GetJSON(ctx, wapiBase+path+sep+v.Encode(), kwHeaders(), &out); err != nil {
		return nil, err
	}
	data, _ := out["data"]
	var dm map[string]json.RawMessage
	json.Unmarshal(data, &dm)
	for _, k := range keys {
		if raw, ok := dm[k]; ok {
			return listFromRaw(raw), nil
		}
	}
	return nil, nil
}

func (s Source) AlbumSongs(ctx context.Context, albumID string, page, limit int) (*music.Page[music.Song], error) {
	list, err := wwwList(ctx, "/album/albumInfo?albumId="+url.QueryEscape(albumID), page, limit, "musicList", "list")
	if err != nil {
		return nil, err
	}
	return mapPage(list, page, limit), nil
}

func (s Source) SonglistSongs(ctx context.Context, listID string, page, limit int) (*music.Page[music.Song], error) {
	list, err := wwwList(ctx, "/playlist/playListInfo?pid="+url.QueryEscape(listID), page, limit, "musicList", "list")
	if err != nil {
		return nil, err
	}
	return mapPage(list, page, limit), nil
}

func (s Source) ArtistSongs(ctx context.Context, artistID string, page, limit int) (*music.Page[music.Song], error) {
	list, err := wwwList(ctx, "/artist/artistMusic?artistid="+url.QueryEscape(artistID), page, limit, "list", "musicList")
	if err != nil {
		return nil, err
	}
	return mapPage(list, page, limit), nil
}

func mapPage(list []map[string]any, page, limit int) *music.Page[music.Song] {
	songs := make([]music.Song, 0, len(list))
	for _, m := range list {
		if sg := songFromMap(m); sg.Songmid != "" {
			songs = append(songs, *sg)
		}
	}
	return &music.Page[music.Song]{
		List: songs, Total: len(songs), Page: page, Limit: limit,
		HasMore: len(songs) >= limit,
	}
}

/* ---------- 歌词 / 封面 / 随机 ---------- */

func (s Source) GetLyric(ctx context.Context, song *music.Song) (*music.Lyrics, error) {
	var out struct {
		Code int `json:"code"`
		Data struct {
			Lrclist []struct {
				Time      string `json:"time"`
				LineLyric string `json:"lineLyric"`
			} `json:"lrclist"`
		} `json:"data"`
	}
	if err := music.GetJSON(ctx, lyricURL+"?musicId="+url.QueryEscape(song.Songmid), nil, &out); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, l := range out.Data.Lrclist {
		t, _ := strconv.ParseFloat(l.Time, 64)
		min := int(t) / 60
		sec := t - float64(min*60)
		b.WriteString(fmtLrcTime(min, sec))
		b.WriteString(l.LineLyric)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return nil, music.ErrUnsupported
	}
	return &music.Lyrics{Lyric: b.String()}, nil
}

func fmtLrcTime(min int, sec float64) string {
	return "[" + pad2(min) + ":" + pad2(int(sec)) + "." + pad2(int((sec-float64(int(sec)))*100)) + "]"
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
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
		Data map[string]any `json:"data"`
	}
	if err := music.GetJSON(ctx, wapiBase+"/music/musicInfo?mid="+url.QueryEscape(song.Songmid)+"&httpsStatus=1", kwHeaders(), &out); err != nil {
		return "", err
	}
	if pic := mstr(out.Data, "pic", "albumpic"); pic != "" {
		return upgradePic(pic), nil
	}
	return "", music.ErrUnsupported
}

// randomPool 随机歌单缓存(进程内),避免每首歌都打一次搜索接口
var randomPool struct {
	mu    sync.Mutex
	songs []music.Song
	seen  map[string]bool
}

// maxRandomPoolSongs 随机池硬上限
const maxRandomPoolSongs = 2000

// RandomSong 随机关键词 + 随机页,取随机一首(优先从缓存池取)
func (s Source) RandomSong(ctx context.Context) (*music.Song, error) {
	randomPool.mu.Lock()
	pool := randomPool.songs
	randomPool.mu.Unlock()

	if len(pool) < 400 {
		if sg, err := s.fillRandomPool(ctx); err == nil && sg != nil {
			return sg, nil
		}
	}

	randomPool.mu.Lock()
	defer randomPool.mu.Unlock()
	if len(randomPool.songs) == 0 {
		return nil, music.ErrUnsupported
	}
	return &randomPool.songs[mrand.Intn(len(randomPool.songs))], nil
}

// fillRandomPool 拉取一个新的随机关键词搜索页入池,并返回一首
func (s Source) fillRandomPool(ctx context.Context) (*music.Song, error) {
	words := strings.Fields(randomSeed)
	keyword := words[mrand.Intn(len(words))]
	page := mrand.Intn(8) + 1
	p, err := s.SearchMusic(ctx, keyword, page, 30)
	if err != nil || len(p.List) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, music.ErrUnsupported
	}
	randomPool.mu.Lock()
	if randomPool.seen == nil {
		randomPool.seen = map[string]bool{}
	}
	added := 0
	for _, sg := range p.List {
		k := sg.Key()
		if !randomPool.seen[k] {
			randomPool.seen[k] = true
			randomPool.songs = append(randomPool.songs, sg)
			added++
		}
	}
	// 池子与去重集都必须有上限:每次填充都会引入新的关键词结果,
	// 只增不减会让长期运行的进程内存持续上涨。
	if len(randomPool.songs) > maxRandomPoolSongs {
		drop := len(randomPool.songs) - maxRandomPoolSongs
		kept := make([]music.Song, len(randomPool.songs)-drop)
		copy(kept, randomPool.songs[drop:])
		randomPool.songs = kept
		// seen 只保留仍在池中的 key
		seen := make(map[string]bool, len(kept))
		for i := range kept {
			seen[kept[i].Key()] = true
		}
		randomPool.seen = seen
	}
	n := len(randomPool.songs)
	if n == 0 {
		randomPool.mu.Unlock()
		return nil, music.ErrUnsupported
	}
	// 在持锁区间内完成随机选取并值拷贝返回,避免解锁后撕裂读共享切片
	sg := randomPool.songs[mrand.Intn(n)]
	randomPool.mu.Unlock()
	return &sg, nil
}
