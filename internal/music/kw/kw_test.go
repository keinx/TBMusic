package kw

import "testing"

func TestUnescapeKW(t *testing.T) {
	cases := []struct{ in, want string }{
		// 上游实际形态:两个反斜杠(fixKWJSON + Unmarshal 后仍残留)
		{`毛不易\\u0026魏大勋`, "毛不易&魏大勋"},
		// 单反斜杠的规范写法
		{`毛不易\u0026段奥娟`, "毛不易&段奥娟"},
		// 连续多段
		{`陈露\\u0026杜江\\u0026戴玉强`, "陈露&杜江&戴玉强"},
		// 普通文本不应被改动
		{"周杰伦", "周杰伦"},
		{"AC\\DC", "AC\\DC"},
		{"100%\\", "100%\\"},
		// 代理对(emoji)
		{`a\\ud83d\\ude00b`, "a😀b"},
	}
	for _, c := range cases {
		if got := unescapeKW(c.in); got != c.want {
			t.Errorf("unescapeKW(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
