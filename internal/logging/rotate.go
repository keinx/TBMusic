// Package logging provides a size-bounded log writer.
package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type RotatingWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func NewWriter(path string, maxBytes int64) (*RotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	w := &RotatingWriter{path: path, max: maxBytes, f: f, size: st.Size()}
	if w.size > w.max {
		if err := w.trimLocked(w.max / 2); err != nil {
			f.Close()
			return nil, err
		}
	}
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(p)
	if w.f == nil {
		return 0, os.ErrClosed
	}
	// Keep the newest bytes even if one script prints more than the whole limit.
	if int64(len(p)) > w.max {
		p = p[int64(len(p))-w.max:]
	}
	if w.size+int64(len(p)) > w.max {
		keep := min(w.max/2, w.max-int64(len(p)))
		if err := w.trimLocked(keep); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, err
	}
	return original, nil
}

// Read only a bounded tail, including when an older version left a huge file.
func (w *RotatingWriter) trimLocked(keep int64) error {
	var data []byte
	if keep > 0 {
		r, err := os.Open(w.path)
		if err != nil {
			return err
		}
		st, err := r.Stat()
		if err == nil {
			start := max(int64(0), st.Size()-keep)
			data, err = io.ReadAll(io.NewSectionReader(r, start, keep))
		}
		r.Close()
		if err != nil {
			return err
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 && i+1 < len(data) {
			data = data[i+1:]
		}
	}
	if err := w.f.Truncate(0); err != nil {
		return err
	}
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.size = 0
	if len(data) > 0 {
		n, err := w.f.Write(data)
		w.size = int64(n)
		return err
	}
	return nil
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
