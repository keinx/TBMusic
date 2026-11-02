package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrBusy = errors.New("上游请求繁忙，请稍后重试")
var metadataHTTPSem = make(chan struct{}, 32)

func acquireMetadataRequest(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case metadataHTTPSem <- struct{}{}:
		return func() { <-metadataHTTPSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, ErrBusy
	}
}

// HTTP 请求辅助:统一 UA、编码处理、JSON 解析

func httpDo(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (int, []byte, error) {
	release, err := acquireMetadataRequest(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer release()
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", DefaultUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := Client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, data, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(data, 200))
	}
	return resp.StatusCode, data, nil
}

func GetJSON(ctx context.Context, rawURL string, headers map[string]string, out any) error {
	_, data, err := httpDo(ctx, http.MethodGet, rawURL, headers, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("JSON 解析失败: %w (%s)", err, truncate(data, 200))
	}
	return nil
}

func PostJSON(ctx context.Context, rawURL string, headers map[string]string, payload, out any) error {
	b, _ := json.Marshal(payload)
	_, data, err := httpDo(ctx, http.MethodPost, rawURL, headers, b)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("JSON 解析失败: %w (%s)", err, truncate(data, 200))
	}
	return nil
}

func GetString(ctx context.Context, rawURL string, headers map[string]string) (int, string, error) {
	status, data, err := httpDo(ctx, http.MethodGet, rawURL, headers, nil)
	return status, string(data), err
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n]
	}
	return s
}

// GetStringSafe 走拨号复检客户端的 GET(用于请求方可控 URL 的场景,防 DNS 重绑定)。
// SafeHTTPClient 没有整体超时(音频流需要长连接),这里用 context 兜住 30s。
func GetStringSafe(ctx context.Context, u string, headers map[string]string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := acquireMetadataRequest(ctx)
	if err != nil {
		return 0, "", err
	}
	defer release()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", DefaultUA)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := SafeHTTPClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, "", fmt.Errorf("歌词请求失败: HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, string(body), nil
}
