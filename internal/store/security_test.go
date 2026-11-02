package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tbmusic/internal/music"
)

func securityStore(t *testing.T) (*Store, *User) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "security.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	u, err := s.CreateUser("listener", "hash-old")
	if err != nil {
		t.Fatal(err)
	}
	return s, u
}

func parallelWrites(n int, fn func(int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; fn(i) }(i)
	}
	close(start)
	wg.Wait()
}

func TestRevocationAndStalePasswordLogin(t *testing.T) {
	s, u := securityStore(t)
	if err := s.CreateSessionChecked("token", u.ID, time.Hour, "hash-old"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUser("token"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("token"); err != nil {
		t.Fatal(err)
	}
	parallelWrites(20, func(int) {
		if _, _, err := s.SessionUser("token"); !errors.Is(err, ErrNotFound) {
			t.Errorf("revoked token authorized: %v", err)
		}
	})
	if err := s.CreateSession("other", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePasswordHash(u.ID, "hash-new"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUser("other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password change kept session: %v", err)
	}
	if err := s.CreateSessionChecked("late-login", u.ID, time.Hour, "hash-old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale password created session: %v", err)
	}
	if err := s.CreateSessionChecked("new-login", u.ID, time.Hour, "hash-new"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRegistrationQuota(t *testing.T) {
	s, _ := securityStore(t)
	var created atomic.Int32
	parallelWrites(12, func(i int) {
		_, err := s.CreateRegisteredUser(fmt.Sprintf("new-%d", i), "hash", "192.0.2.1")
		if err == nil {
			created.Add(1)
		} else if !errors.Is(err, ErrRegistrationLimit) {
			t.Errorf("unexpected error: %v", err)
		}
	})
	if created.Load() != 3 || s.CountIPEvents("reg", "192.0.2.1") != 3 {
		t.Fatalf("registration quota failed: %d", created.Load())
	}
}

func TestConcurrentLibraryQuotasAndCompletePagination(t *testing.T) {
	s, u := securityStore(t)
	pl, err := s.EnsureDefaultPlaylist(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed close to each limit efficiently, then race real public store methods.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < MaxPlaylists-2; i++ {
		if _, err := tx.Exec(`INSERT INTO playlists(user_id,name,created_at) VALUES(?,?,0)`, u.ID, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < MaxPlaylistSongs-1; i++ {
		if _, err := tx.Exec(`INSERT INTO playlist_songs(playlist_id,source,song_id,music_info,added_at) VALUES(?,'wy',?,'{}',0)`, pl.ID, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < MaxBlacklistSongs-1; i++ {
		if _, err := tx.Exec(`INSERT INTO blacklist(user_id,source,song_id,music_info,created_at) VALUES(?,'wy',?,'{}',0)`, u.ID, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var playlists, songs, black atomic.Int32
	parallelWrites(10, func(i int) {
		if _, err := s.CreatePlaylist(u.ID, fmt.Sprintf("extra-%d", i), false); err == nil {
			playlists.Add(1)
		} else if !IsQuotaExceeded(err) {
			t.Errorf("playlist: %v", err)
		}
		song := &music.Song{Source: "wy", Songmid: fmt.Sprintf("extra-%d", i), Name: "test"}
		if _, _, err := s.AddSongToPlaylist(pl.ID, u.ID, song); err == nil {
			songs.Add(1)
		} else if !IsQuotaExceeded(err) {
			t.Errorf("song: %v", err)
		}
		if _, err := s.AddBlacklist(u.ID, song); err == nil {
			black.Add(1)
		} else if !IsQuotaExceeded(err) {
			t.Errorf("blacklist: %v", err)
		}
	})
	if playlists.Load() != 1 || songs.Load() != 1 || black.Load() != 1 {
		t.Fatalf("quota overflow: %d %d %d", playlists.Load(), songs.Load(), black.Load())
	}
	if added, _, err := s.AddSongToPlaylist(pl.ID, u.ID, &music.Song{Source: "wy", Songmid: "0"}); err != nil || added {
		t.Fatalf("duplicate at quota: %v %v", added, err)
	}
	for _, tc := range []struct {
		want int
		page func(int64, int) ([]SongEntry, error)
	}{
		{MaxPlaylistSongs, func(cursor int64, limit int) ([]SongEntry, error) {
			return s.PlaylistSongsPage(pl.ID, u.ID, "", cursor, limit)
		}},
		{MaxBlacklistSongs, func(cursor int64, limit int) ([]SongEntry, error) { return s.BlacklistPage(u.ID, "", cursor, limit) }},
	} {
		seen := map[int64]bool{}
		var cursor int64
		for {
			page, err := tc.page(cursor, 200)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			for _, entry := range page {
				if seen[entry.ID] {
					t.Fatal("duplicate page entry")
				}
				seen[entry.ID] = true
			}
			cursor = page[len(page)-1].ID
		}
		if len(seen) != tc.want {
			t.Fatalf("list truncated: got %d want %d", len(seen), tc.want)
		}
	}
	other, err := s.CreateUser("other", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlaylistSongsPage(pl.ID, other.ID, "", 0, 200); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user access: %v", err)
	}
}

func TestParentGuardsAndCascades(t *testing.T) {
	s, u := securityStore(t)
	pl, err := s.EnsureDefaultPlaylist(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	song := &music.Song{Source: "wy", Songmid: "song", Name: "test"}
	if _, _, err := s.AddSongToPlaylist(pl.ID, u.ID, song); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBlacklist(u.ID, song); err != nil {
		t.Fatal(err)
	}
	if err := s.AddHistory(u.ID, song); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession("token", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	// Exercise the database guarantees even when deletion bypasses the helpers.
	if _, err := s.db.Exec(`DELETE FROM users WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "playlists", "playlist_songs", "blacklist", "play_history"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan in %s: %d %v", table, count, err)
		}
	}
	for _, query := range []string{
		`INSERT INTO sessions VALUES('late',999,1,1)`,
		`INSERT INTO playlists(user_id,name,created_at) VALUES(999,'late',1)`,
		`INSERT INTO playlist_songs(playlist_id,source,song_id,music_info,added_at) VALUES(999,'wy','late','{}',1)`,
		`INSERT INTO blacklist(user_id,source,song_id,music_info,created_at) VALUES(999,'wy','late','{}',1)`,
		`INSERT INTO play_history(user_id,source,song_id,name,singer,music_info,played_at) VALUES(999,'wy','late','','','{}',1)`,
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("orphan insert accepted: %s", query)
		}
	}
}
