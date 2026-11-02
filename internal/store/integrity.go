package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MaxPlaylists      = 50
	MaxPlaylistSongs  = 5000
	MaxBlacklistSongs = 2000
)

var ErrRegistrationLimit = errors.New("registration limit reached")

func IsQuotaExceeded(err error) bool {
	return err != nil && strings.Contains(err.Error(), "TBMUSIC_QUOTA")
}

// Existing installations have tables without REFERENCES clauses. Database
// triggers enforce the same parent/child guarantees without rebuilding tables
// or reusing deleted AUTOINCREMENT IDs during migration.
func (s *Store) migrateIntegrity() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	parents := []struct{ table, column, parent string }{
		{"sessions", "user_id", "users"}, {"playlists", "user_id", "users"},
		{"playlist_songs", "playlist_id", "playlists"},
		{"blacklist", "user_id", "users"}, {"play_history", "user_id", "users"},
	}
	for _, p := range parents {
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s NOT IN (SELECT id FROM %s)`, p.table, p.column, p.parent)); err != nil {
			return err
		}
		for _, operation := range []string{"INSERT", "UPDATE"} {
			q := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS guard_%s_%s BEFORE %s ON %s
			 WHEN NOT EXISTS (SELECT 1 FROM %s WHERE id=NEW.%s)
			 BEGIN SELECT RAISE(ABORT, 'TBMUSIC_PARENT'); END`, p.table, operation, operation, p.table, p.parent, p.column)
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		q := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS cascade_%s AFTER DELETE ON %s
		 BEGIN DELETE FROM %s WHERE %s=OLD.id; END`, p.table, p.parent, p.table, p.column)
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	quotas := []struct {
		table, column, duplicate string
		limit                    int
	}{
		{"playlists", "user_id", "name=NEW.name", MaxPlaylists},
		{"playlist_songs", "playlist_id", "source=NEW.source AND song_id=NEW.song_id", MaxPlaylistSongs},
		{"blacklist", "user_id", "source=NEW.source AND song_id=NEW.song_id", MaxBlacklistSongs},
	}
	for _, q := range quotas {
		stmt := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS quota_%s BEFORE INSERT ON %s
		 WHEN NOT EXISTS (SELECT 1 FROM %s WHERE %s=NEW.%s AND %s)
		 AND (SELECT COUNT(*) FROM %s WHERE %s=NEW.%s) >= %d
		 BEGIN SELECT RAISE(ABORT, 'TBMUSIC_QUOTA'); END`, q.table, q.table, q.table, q.column, q.column, q.duplicate, q.table, q.column, q.column, q.limit)
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Account creation and its IP quota reservation succeed or roll back together.
func (s *Store) CreateRegisteredUser(username, passwordHash, ip string) (*User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ip_events WHERE kind='reg' AND ip=? AND ts>?`, ip, now-3600).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 3 {
		return nil, ErrRegistrationLimit
	}
	res, err := tx.Exec(`INSERT INTO users(username,password_hash,is_admin,created_at) VALUES(?,?,0,?)`, username, passwordHash, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO playlists(user_id,name,is_default,created_at) VALUES(?,'默认歌单',1,?)`, id, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO ip_events(kind,ip,ts) VALUES('reg',?,?)`, ip, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM ip_events WHERE ts<?`, now-4*3600); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, CreatedAt: now}, nil
}
