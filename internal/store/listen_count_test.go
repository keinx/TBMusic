package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"tbmusic/internal/music"
)

func TestListenCountSurvivesHistoryLimitClearAndConcurrentPlays(t *testing.T) {
	s, u := securityStore(t)
	for i := 0; i < 505; i++ {
		if _, err := s.RecordPlay(u.ID, &music.Song{Source: "wy", Songmid: fmt.Sprint(i), Name: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	parallelWrites(30, func(i int) {
		if _, err := s.RecordPlay(u.ID, &music.Song{Source: "wy", Songmid: "same", Name: "test"}); err != nil {
			t.Error(err)
		}
	})
	history, err := s.ListHistory(u.ID, "", 500)
	if err != nil || len(history) != 500 {
		t.Fatalf("history=%d err=%v", len(history), err)
	}
	if err := s.ClearHistory(u.ID); err != nil {
		t.Fatal(err)
	}
	user, err := s.GetUserByID(u.ID)
	if err != nil || user.ListenCount != 535 {
		t.Fatalf("user=%+v err=%v", user, err)
	}
	users, err := s.ListUsers()
	if err != nil || users[0].ListenCount != 535 || users[0].HistoryCount != 0 {
		t.Fatalf("stats=%+v err=%v", users, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON play_history BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPlay(u.ID, &music.Song{Source: "wy", Songmid: "fail"}); err == nil {
		t.Fatal("failed write accepted")
	}
	user, err = s.GetUserByID(u.ID)
	if err != nil || user.ListenCount != 535 {
		t.Fatalf("failed write changed count: %+v %v", user, err)
	}
}

func TestListenCountMigrationPreservesExistingDataAndRunsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	u, err := s.CreateUser("existing", "unchanged-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession("existing-session", u.ID, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := s.AddHistory(u.ID, &music.Song{Source: "wy", Songmid: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`ALTER TABLE users DROP COLUMN listen_count`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := s.SessionUser("existing-session")
	if err != nil || loaded.ListenCount != 2 {
		t.Fatalf("migrated session: %+v %v", loaded, err)
	}
	_, hash, err := s.GetUserByUsername("existing")
	if err != nil || hash != "unchanged-password-hash" {
		t.Fatalf("account changed: %v", err)
	}
	if count, err := s.RecordPlay(u.ID, &music.Song{Source: "wy", Songmid: "a", Name: "a"}); err != nil || count != 3 {
		t.Fatalf("repeat count=%d err=%v", count, err)
	}
	if err := s.ClearHistory(u.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = s.GetUserByID(u.ID)
	if err != nil || loaded.ListenCount != 3 {
		t.Fatalf("migration ran twice: %+v %v", loaded, err)
	}
	var playlists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playlists WHERE user_id=?`, u.ID).Scan(&playlists); err != nil || playlists != 1 {
		t.Fatalf("playlist lost: %d %v", playlists, err)
	}
}
