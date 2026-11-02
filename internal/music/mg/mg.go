// Package mg 咪咕内置音源:搜索(pd.musicapp.migu.cn)
package mg

import (
	"context"
	"net/url"
	"strings"

	"tbmusic/internal/music"
)

const searchURL = "https://pd.musicapp.migu.cn/MIGUM2.0/v1.0/content/search_all.do"

type Source struct{ music.Base }

var _ music.Source = Source{}

func (Source) ID() string            { return "mg" }
func (Source) Name() string          { return "咪咕" }
func (Source) SearchTypes() []string { return []string{"music"} }

func mstr(m map[string]any, keys ...string) string { return music.Str(m, keys...) }

func (Source) SearchMusic(ctx context.Context, keyword string, page, limit int) (*music.Page[music.Song], error) {
	u := searchURL + "?text=" + url.QueryEscape(keyword) +
		"&pageNo=" + itoa(page) + "&pageSize=" + itoa(limit) +
		"&searchSwitch=%7B%22song%22%3A1%7D"
	var out struct {
		SongResultData struct {
			TotalCount string           `json:"totalCount"`
			Result     []map[string]any `json:"result"`
		} `json:"songResultData"`
	}
	h := map[string]string{"User-Agent": music.DefaultUA, "Referer": "https://m.music.migu.cn/"}
	if err := music.GetJSON(ctx, u, h, &out); err != nil {
		return nil, err
	}
	songs := make([]music.Song, 0, len(out.SongResultData.Result))
	for _, m := range out.SongResultData.Result {
		sg := songFromMap(m)
		if sg.CopyrightID != "" {
			songs = append(songs, *sg)
		}
	}
	total := 0
	if t := out.SongResultData.TotalCount; t != "" {
		total = atoiSafe(t)
	}
	return &music.Page[music.Song]{
		List: songs, Total: total, Page: page, Limit: limit,
		HasMore: music.HasMore(page, limit, total, len(songs)),
	}, nil
}

func songFromMap(m map[string]any) *music.Song {
	var singers []string
	if arr, ok := m["singers"].([]any); ok {
		for _, s := range arr {
			if sm, ok := s.(map[string]any); ok {
				if n := mstr(sm, "name"); n != "" {
					singers = append(singers, n)
				}
			}
		}
	}
	// 专辑信息在 albums 数组里(上游并没有 album / albumName / albumId 三个平铺字段)
	albumID, albumName := "", ""
	if arr, ok := m["albums"].([]any); ok && len(arr) > 0 {
		if am, ok := arr[0].(map[string]any); ok {
			albumID = mstr(am, "id")
			albumName = mstr(am, "name")
		}
	}
	sec := atoiSafe(mstr(m, "interval", "length", "duration"))
	// 音质能力在 newRateFormats / rateFormats 的 formatType 里:
	// PQ=标准 HQ=高品质 SQ=无损 ZQ=Hi-Res,旧字段 hq/sq/hires 上游已不再下发
	qualities := []string{"128k"}
	for _, key := range []string{"newRateFormats", "rateFormats"} {
		arr, ok := m[key].([]any)
		if !ok {
			continue
		}
		for _, f := range arr {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			switch strings.ToUpper(mstr(fm, "formatType")) {
			case "HQ":
				qualities = appendUnique(qualities, "320k")
			case "SQ":
				qualities = appendUnique(qualities, "flac")
			case "ZQ", "ZQ24":
				qualities = appendUnique(qualities, "flac24bit")
			}
		}
	}
	pic := mstr(m, "cover", "albumCover", "pic")
	if pic == "" {
		if items, ok := m["imgItems"].([]any); ok {
			for _, it := range items {
				if im, ok := it.(map[string]any); ok {
					if u := mstr(im, "img"); u != "" {
						pic = u
						break
					}
				}
			}
		}
	}
	pic = strings.ReplaceAll(pic, "http://", "https://")
	if strings.Contains(pic, "{size}") {
		pic = strings.ReplaceAll(pic, "{size}", "500")
	}
	return &music.Song{
		Source:      "mg",
		Songmid:     mstr(m, "copyrightId"),
		SongID:      mstr(m, "songId", "id"),
		CopyrightID: mstr(m, "copyrightId"),
		AlbumID:     albumID,
		AlbumName:   albumName,
		Name:        mstr(m, "name", "songName"),
		Singer:      strings.Join(singers, "/"),
		Interval:    music.FormatInterval(sec),
		Pic:         pic,
		LrcURL:      strings.ReplaceAll(mstr(m, "lrcUrl", "lyricUrl"), "http://", "https://"),
		Qualities:   qualities,
	}
}

// appendUnique 追加去重
func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func (s Source) GetLyric(ctx context.Context, song *music.Song) (*music.Lyrics, error) {
	u := song.LrcURL
	if u == "" {
		return nil, music.ErrUnsupported
	}
	// lrcUrl 来自请求方,防 SSRF:仅允许公网地址
	if err := music.SafeOutboundURLContext(ctx, u); err != nil {
		return nil, err
	}
	_, body, err := music.GetStringSafe(ctx, u, map[string]string{
		"User-Agent": music.DefaultUA, "Referer": "https://m.music.migu.cn/",
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, music.ErrUnsupported
	}
	return &music.Lyrics{Lyric: body}, nil
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

func atoiSafe(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
