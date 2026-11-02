// Package kg 酷狗内置音源:搜索(songsearch.kugou.com)、歌词(krcs.kugou.com)
package kg

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"

	"tbmusic/internal/music"
)

type Source struct{ music.Base }

var _ music.Source = Source{}

func (Source) ID() string            { return "kg" }
func (Source) Name() string          { return "酷狗" }
func (Source) SearchTypes() []string { return []string{"music"} }

func mstr(m map[string]any, keys ...string) string { return music.Str(m, keys...) }
func mint(m map[string]any, keys ...string) int    { return music.Int(m, keys...) }

func (Source) SearchMusic(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	u := "https://songsearch.kugou.com/song_search_v2?keyword=" + url.QueryEscape(keyword) +
		"&page=" + itoa(page) + "&pagesize=" + itoa(limit)
	var out struct {
		Status int `json:"status"`
		Data   struct {
			Total int              `json:"total"`
			Lists []map[string]any `json:"lists"`
		} `json:"data"`
	}
	if err := music.GetJSON(ctx, u, map[string]string{"User-Agent": music.DefaultUA}, &out); err != nil {
		return nil, err
	}
	songs := make([]music.Song, 0, len(out.Data.Lists))
	for _, m := range out.Data.Lists {
		sg := songFromMap(m)
		if sg.Hash != "" {
			songs = append(songs, *sg)
		}
	}
	return &music.Page[music.Song]{
		List: songs, Total: out.Data.Total, Page: page, Limit: limit,
		HasMore: music.HasMore(page, limit, out.Data.Total, len(out.Data.Lists)),
	}, nil
}

func songFromMap(m map[string]any) *music.Song {
	qualities := []string{"128k"}
	if mstr(m, "HQFileHash") != "" {
		qualities = append(qualities, "320k")
	}
	if mstr(m, "SQFileHash") != "" {
		qualities = append(qualities, "flac")
	}
	name := mstr(m, "SongName")
	singer := mstr(m, "SingerName")
	// "歌手 - 歌名" 兜底拆分
	if name == "" {
		if fn := mstr(m, "FileName"); fn != "" {
			if i := strings.Index(fn, " - "); i > 0 {
				singer, name = fn[:i], fn[i+3:]
			} else {
				name = fn
			}
		}
	}
	// 酷狗封面模板形如 http://imge.kugou.com/stdmusic/{size}/xxx.jpg:
	// 统一换成 https(页面若走 https 反代,http 图会被浏览器按混合内容拦截),
	// 并把尺寸提到 480,列表缩略图与播放页大图都够清晰
	pic := strings.ReplaceAll(mstr(m, "Image", "AlbumImage"), "{size}", "480")
	pic = strings.ReplaceAll(pic, "http://", "https://")
	return &music.Song{
		Source:    "kg",
		Songmid:   mstr(m, "MixSongID"),
		SongID:    mstr(m, "MixSongID"),
		Hash:      mstr(m, "FileHash"),
		AlbumID:   mstr(m, "AlbumID"),
		AlbumName: mstr(m, "AlbumName"),
		Name:      name,
		Singer:    singer,
		Interval:  music.FormatInterval(mint(m, "Duration")),
		Pic:       pic,
		Qualities: qualities,
	}
}

func (s Source) GetLyric(ctx context.Context, song *music.Song) (*music.Lyrics, error) {
	hash := song.Hash
	if hash == "" {
		return nil, music.ErrUnsupported
	}
	sec := parseIntervalSec(song.Interval)
	var search struct {
		Candidates []struct {
			ID        string `json:"id"`
			AccessKey string `json:"accesskey"`
		} `json:"candidates"`
	}
	su := "https://krcs.kugou.com/search?ver=1&man=yes&client=mobi&keyword=" + url.QueryEscape(hash) +
		"&duration=" + itoa(sec*1000) + "&hash=" + url.QueryEscape(hash)
	if err := music.GetJSON(ctx, su, map[string]string{"User-Agent": music.DefaultUA}, &search); err != nil {
		return nil, err
	}
	if len(search.Candidates) == 0 {
		return nil, music.ErrUnsupported
	}
	c := search.Candidates[0]
	var dl struct {
		Status  int    `json:"status"`
		Content string `json:"content"`
	}
	du := "https://krcs.kugou.com/download?ver=1&client=mobi&id=" + url.QueryEscape(c.ID) +
		"&accesskey=" + url.QueryEscape(c.AccessKey) + "&fmt=lrc&charset=utf8"
	if err := music.GetJSON(ctx, du, map[string]string{"User-Agent": music.DefaultUA}, &dl); err != nil {
		return nil, err
	}
	if dl.Content == "" {
		return nil, music.ErrUnsupported
	}
	lrc := dl.Content
	if b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(lrc, "\n", "")); err == nil {
		lrc = string(b)
	}
	return &music.Lyrics{Lyric: lrc}, nil
}

func (s Source) GetPic(ctx context.Context, song *music.Song) (string, error) {
	if song.Pic != "" {
		// 请求方可控,校验 scheme 防异常协议(浏览器 CSP 亦有兜底)
		if u, err := url.Parse(song.Pic); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", music.ErrUnsupported
		}
		return song.Pic, nil
	}
	return "", music.ErrUnsupported
}

func itoa(n int) string { return music.Itoa(n) }

func parseIntervalSec(iv string) int {
	parts := strings.Split(iv, ":")
	if len(parts) != 2 {
		return 0
	}
	min, sec := 0, 0
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		if i == 0 {
			min = n
		} else {
			sec = n
		}
	}
	return min*60 + sec
}
