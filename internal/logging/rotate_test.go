package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotateKeepsWithinLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.log")
	w, err := NewWriter(p, 4096)
	if err != nil {
		t.Fatal(err)
	}
	line := append(bytes.Repeat([]byte("x"), 200), '\n')
	for i := 0; i < 100; i++ { // 写入 ~20KB
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 4096 {
		t.Fatalf("日志超过上限: %d", st.Size())
	}
	b, _ := os.ReadFile(p)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("截断后未保持整行结尾, tail=%q", string(b[len(b)-5:]))
	}
	t.Logf("最终大小 %d 字节", st.Size())
}

func TestOversizedEntryAndExistingFileRemainBounded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.log")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), 8192), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := NewWriter(p, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, size := range []int{512, 100, 1, 128, 129} {
		n, err := w.Write(bytes.Repeat([]byte("x"), size))
		if err != nil || n != size {
			t.Fatalf("write=%d %v", n, err)
		}
		st, err := os.Stat(p)
		if err != nil || st.Size() > 128 {
			t.Fatalf("bound: %v %v", st, err)
		}
	}
}
