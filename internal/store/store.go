// Package store SQLite 持久化:用户、会话、收藏歌单、拉黑、播放历史
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"

	"tbmusic/internal/music"
)

var ErrNotFound = errors.New("not found")

// ErrDuplicate 唯一约束冲突(用户名重复、歌单重名等)
var ErrDuplicate = errors.New("duplicate")

// ErrDefaultPlaylist 默认歌单不允许删除
var ErrDefaultPlaylist = errors.New("默认歌单不可删除")

// ErrProtectedUser 超级管理员账户受保护(不可删除)
var ErrProtectedUser = errors.New("超级管理员账户不可删除")

// ErrAdminPasswordReset 超管密码不允许由后台重置
var ErrAdminPasswordReset = errors.New("超级管理员密码请在个人设置中修改")

// IsDuplicate 判断错误是否为唯一约束冲突。
// 不依赖错误文案:按 SQLite 扩展错误码判定(19=CONSTRAINT, 1555=PRIMARYKEY, 2067=UNIQUE)。
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case 19, 1555, 2067:
			return true
		}
	}
	return errors.Is(err, ErrDuplicate)
}

type Store struct {
	db          *sql.DB
	sessionDays int
}

// SetSessionDays 设置会话滑动续期天数
func (s *Store) SetSessionDays(days int) {
	if days > 0 {
		s.sessionDays = days
	}
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc sqlite 并发写易锁库,串行最稳
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions(
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS playlists(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			UNIQUE(user_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS playlist_songs(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			playlist_id INTEGER NOT NULL,
			source TEXT NOT NULL,
			song_id TEXT NOT NULL,
			music_info TEXT NOT NULL,
			added_at INTEGER NOT NULL,
			UNIQUE(playlist_id, source, song_id)
		)`,
		`CREATE TABLE IF NOT EXISTS blacklist(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			source TEXT NOT NULL,
			song_id TEXT NOT NULL,
			music_info TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(user_id, source, song_id)
		)`,
		`CREATE TABLE IF NOT EXISTS ip_events(
			kind TEXT NOT NULL,
			ip TEXT NOT NULL,
			ts INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ip_events ON ip_events(kind, ip, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_ip_events_ts ON ip_events(ts)`,
		`CREATE TABLE IF NOT EXISTS ip_bans(
			ip TEXT PRIMARY KEY,
			until INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS play_history(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			source TEXT NOT NULL,
			song_id TEXT NOT NULL,
			name TEXT NOT NULL,
			singer TEXT NOT NULL,
			music_info TEXT NOT NULL,
			played_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_history_user ON play_history(user_id, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_plsongs_pl ON playlist_songs(playlist_id, id DESC)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("建表失败: %w", err)
		}
	}
	// 兼容旧库:补充备注列(先查表结构,不依赖错误文案)
	hasRemark, err := s.hasColumn("users", "remark")
	if err != nil {
		return fmt.Errorf("检查 users 表结构失败: %w", err)
	}
	if !hasRemark {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN remark TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("迁移失败(remark): %w", err)
		}
	}
	// admin 为唯一的超级管理员账户
	if _, err := s.db.Exec(`UPDATE users SET is_admin=0 WHERE username <> 'admin'`); err != nil {
		return fmt.Errorf("迁移失败(is_admin): %w", err)
	}
	if err := s.migrateIntegrity(); err != nil {
		return err
	}
	return s.migrateListenCount()
}

// Add the lifetime counter and seed retained history exactly once, atomically.
// Older history was deduplicated and capped at 500; deleted plays cannot be recovered.
func (s *Store) migrateListenCount() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='listen_count'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN listen_count INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET listen_count=(SELECT COUNT(*) FROM play_history h WHERE h.user_id=users.id)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hasColumn 查询表是否存在指定列(table/col 仅由本包常量传入)
func (s *Store) hasColumn(table, col string) (bool, error) {
	rows, err := s.db.Query(`SELECT COUNT(*) FROM pragma_table_info('`+table+`') WHERE name=?`, col)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return false, err
		}
	}
	return n > 0, rows.Err()
}

// EnsureAdmin 确保超级管理员 admin 存在(密码仅在首次创建时设置)
func (s *Store) EnsureAdmin(defaultPasswordHash string) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM users WHERE username='admin' COLLATE NOCASE`).Scan(&id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE users SET is_admin=1 WHERE id=?`, id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users(username, password_hash, is_admin, created_at) VALUES('admin', ?, 1, ?)`,
		defaultPasswordHash, time.Now().Unix())
	if err != nil {
		return err
	}
	return nil
}

/* ---------- 密码 ---------- */

// ErrBadPassword 旧密码校验失败
var ErrBadPassword = errors.New("旧密码错误")

// VerifyPassword 只做一次 bcrypt 比对,供「先验证旧密码、再哈希新密码」的改密流程使用,
// 避免旧密码错误时仍白白消耗一次 bcrypt 计算(CPU 放大攻击面)。
func (s *Store) VerifyPassword(userID int64, password string) error {
	_, err := s.VerifiedPasswordHash(userID, password)
	return err
}

func (s *Store) VerifiedPasswordHash(userID int64, password string) (string, error) {
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", ErrBadPassword
	}
	return hash, nil
}

var ErrPasswordChanged = errors.New("密码版本已变更")

func (s *Store) ChangePasswordHash(userID int64, newPasswordHash, expectedHash string) error {
	if expectedHash == "" {
		return ErrBadPassword
	}
	return s.updatePasswordHash(userID, newPasswordHash, expectedHash)
}

// UpdatePasswordHash 写入新密码哈希并吊销该用户全部会话
func (s *Store) UpdatePasswordHash(userID int64, newPasswordHash string) error {
	return s.updatePasswordHash(userID, newPasswordHash, "")
}

func (s *Store) updatePasswordHash(userID int64, newPasswordHash, expectedHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `UPDATE users SET password_hash=? WHERE id=?`
	args := []any{newPasswordHash, userID}
	if expectedHash != "" {
		query += ` AND password_hash=?`
		args = append(args, expectedHash)
	}
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if expectedHash != "" {
			return ErrPasswordChanged
		}
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

/* ---------- 用户管理(超管) ---------- */

func (s *Store) ListUsers() ([]UserWithStats, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.username, u.is_admin, u.created_at, u.remark, u.listen_count,
			(SELECT COUNT(*) FROM playlist_songs ps JOIN playlists p ON p.id = ps.playlist_id WHERE p.user_id = u.id) AS fav_cnt,
			(SELECT COUNT(*) FROM blacklist b WHERE b.user_id = u.id) AS black_cnt,
			(SELECT COUNT(*) FROM play_history h WHERE h.user_id = u.id) AS hist_cnt
		FROM users u ORDER BY u.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserWithStats
	for rows.Next() {
		var u UserWithStats
		var admin int
		if err := rows.Scan(&u.ID, &u.Username, &admin, &u.CreatedAt, &u.Remark, &u.ListenCount, &u.FavCount, &u.BlackCount, &u.HistoryCount); err != nil {
			return nil, err
		}
		u.IsAdmin = admin == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

type UserWithStats struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	IsAdmin      bool   `json:"isAdmin"`
	CreatedAt    int64  `json:"createdAt"`
	Remark       string `json:"remark"`
	FavCount     int    `json:"favCount"`
	BlackCount   int    `json:"blackCount"`
	HistoryCount int    `json:"historyCount"`
	ListenCount  int64  `json:"listenCount"`
}

// UpdateUserRemark 超管为用户设置备注
func (s *Store) UpdateUserRemark(userID int64, remark string) error {
	res, err := s.db.Exec(`UPDATE users SET remark=? WHERE id=?`, remark, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetUserPassword 超管重置用户密码(不适用于 admin 自身,admin 请用个人设置)
func (s *Store) ResetUserPassword(userID int64, newPasswordHash string) error {
	var username string
	if err := s.db.QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&username); err != nil {
		return ErrNotFound
	}
	if strings.EqualFold(username, "admin") {
		return ErrAdminPasswordReset
	}
	return s.UpdatePasswordHash(userID, newPasswordHash)
}

// DeleteUserCascade 删除用户及其全部数据(会话/歌单/收藏/拉黑/历史)
func (s *Store) DeleteUserCascade(userID int64) error {
	var username string
	var admin int
	err := s.db.QueryRow(`SELECT username, is_admin FROM users WHERE id=?`, userID).Scan(&username, &admin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if admin == 1 || strings.EqualFold(username, "admin") {
		return ErrProtectedUser
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`DELETE FROM playlist_songs WHERE playlist_id IN (SELECT id FROM playlists WHERE user_id=?)`,
		`DELETE FROM playlists WHERE user_id=?`,
		`DELETE FROM blacklist WHERE user_id=?`,
		`DELETE FROM play_history WHERE user_id=?`,
		`DELETE FROM sessions WHERE user_id=?`,
		`DELETE FROM users WHERE id=?`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, userID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

/* ---------- 配额统计 ---------- */

// CountPlaylists 用户歌单总数
func (s *Store) CountPlaylists(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM playlists WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

// CountPlaylistSongs 歌单内歌曲数。
// 必须带 userID 做归属校验:否则任意登录用户可用别人的 playlist_id
// 探测该歌单是否存在(404 / 上限提示的差异)及其容量。
func (s *Store) CountPlaylistSongs(playlistID, userID int64) (int, error) {
	if _, err := s.getPlaylist(playlistID, userID); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM playlist_songs WHERE playlist_id=?`, playlistID).Scan(&n)
	return n, err
}

// CountBlacklist 用户黑名单条数
func (s *Store) CountBlacklist(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM blacklist WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

/* ---------- users / sessions ---------- */

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	IsAdmin     bool   `json:"isAdmin"`
	CreatedAt   int64  `json:"createdAt"`
	ListenCount int64  `json:"listenCount"`
}

func (s *Store) CreateUser(username, passwordHash string) (*User, error) {
	// 说明:admin 由启动时 EnsureAdmin 创建,此处恒为普通用户
	// (历史上的"首个注册用户为管理员"分支因 EnsureAdmin 必先执行而不可达)
	isAdmin := 0
	res, err := s.db.Exec(`INSERT INTO users(username, password_hash, is_admin, created_at) VALUES(?,?,?,?)`,
		username, passwordHash, isAdmin, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	u := &User{ID: id, Username: username, IsAdmin: isAdmin == 1, CreatedAt: time.Now().Unix()}
	// 默认歌单创建失败不阻塞注册(登录时会补建)
	if _, err := s.CreatePlaylist(u.ID, "默认歌单", true); err != nil {
		log.Printf("[store] 用户 %s 默认歌单创建失败(登录时会补建): %v", username, err)
	}
	return u, nil
}

func (s *Store) GetUserByUsername(username string) (*User, string, error) {
	row := s.db.QueryRow(`SELECT id, username, password_hash, is_admin, created_at, listen_count FROM users WHERE username = ? COLLATE NOCASE`, username)
	u := &User{}
	var hash string
	var admin int
	if err := row.Scan(&u.ID, &u.Username, &hash, &admin, &u.CreatedAt, &u.ListenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	u.IsAdmin = admin == 1
	return u, hash, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, is_admin, created_at, listen_count FROM users WHERE id=?`, id)
	u := &User{}
	var admin int
	if err := row.Scan(&u.ID, &u.Username, &admin, &u.CreatedAt, &u.ListenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = admin == 1
	return u, nil
}

func (s *Store) CreateSession(token string, userID int64, ttl time.Duration) error {
	return s.CreateSessionChecked(token, userID, ttl, "")
}

// Checking the hash again in the INSERT prevents an old in-flight login from
// creating a new session after a concurrent password change.
func (s *Store) CreateSessionChecked(token string, userID int64, ttl time.Duration, passwordHash string) error {
	now := time.Now()
	query := `INSERT INTO sessions(token,user_id,expires_at,created_at)
  SELECT ?,id,?,? FROM users WHERE id=?`
	args := []any{token, now.Add(ttl).Unix(), now.Unix(), userID}
	if passwordHash != "" {
		query += ` AND password_hash=?`
		args = append(args, passwordHash)
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	return nil
}

// SessionUser 在数据库中校验会话并返回用户;滑动续期。
// renewed 为 true 时调用方需重发 Cookie(否则浏览器端仍按旧的 Max-Age 过期)。
func (s *Store) SessionUser(token string) (*User, bool, error) {
	now := time.Now().Unix()
	u := &User{}
	var expires int64
	var admin int
	err := s.db.QueryRow(`SELECT u.id, u.username, u.is_admin, u.created_at, u.listen_count, s.expires_at
  FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token=? AND s.expires_at>?`, token, now).
		Scan(&u.ID, &u.Username, &admin, &u.CreatedAt, &u.ListenCount, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	u.IsAdmin = admin == 1
	if expires-now >= int64(7*24*time.Hour.Seconds()) {
		return u, false, nil
	}
	days := s.sessionDays
	if days <= 0 {
		days = 30
	}
	res, err := s.db.Exec(`UPDATE sessions SET expires_at=? WHERE token=? AND expires_at>?`,
		time.Now().Add(time.Duration(days)*24*time.Hour).Unix(), token, now)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, false, ErrNotFound
	}
	return u, true, nil
}

// A revoked session is never kept in an independent authorization cache.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

/* ---------- IP 注册/登录限制 ---------- */

// ipEventCleanup 清理过期事件(4 小时保留窗)
func (s *Store) ipEventCleanup(now int64) {
	s.db.Exec(`DELETE FROM ip_events WHERE ts < ?`, now-4*3600)
}

// CountIPEvents 统计该 IP 在 1 小时窗口内的事件数
func (s *Store) CountIPEvents(kind, ip string) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM ip_events WHERE kind=? AND ip=? AND ts > ?`,
		kind, ip, time.Now().Unix()-3600).Scan(&n)
	return n
}

// AddIPEvent 记录一次事件(注册/登录失败),顺带清理过期行
func (s *Store) AddIPEvent(kind, ip string) {
	now := time.Now().Unix()
	s.db.Exec(`INSERT INTO ip_events(kind, ip, ts) VALUES(?,?,?)`, kind, ip, now)
	s.ipEventCleanup(now)
}

// IPBanned 该 IP 是否在封锁期内
func (s *Store) IPBanned(ip string) (bool, int64) {
	var until int64
	err := s.db.QueryRow(`SELECT until FROM ip_bans WHERE ip=?`, ip).Scan(&until)
	if err != nil || until <= time.Now().Unix() {
		if err == nil {
			// 仅清已过期行,防与并发 BanIP 竞态抹掉新封锁
			s.db.Exec(`DELETE FROM ip_bans WHERE ip=? AND until<=?`, ip, time.Now().Unix())
		}
		return false, 0
	}
	return true, until
}

// BanIP 封锁 IP 至指定时间(覆盖旧封锁)
func (s *Store) BanIP(ip string, until time.Time) {
	s.db.Exec(`INSERT INTO ip_bans(ip, until) VALUES(?,?)
		ON CONFLICT(ip) DO UPDATE SET until=excluded.until`, ip, until.Unix())
}

// ClearIPEvents 清空该 IP 的登录失败记录(成功登录时调用)
func (s *Store) ClearIPEvents(kind, ip string) {
	s.db.Exec(`DELETE FROM ip_events WHERE kind=? AND ip=?`, kind, ip)
}

/* ---------- playlists ---------- */

type Playlist struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Count     int    `json:"count"`
	CreatedAt int64  `json:"createdAt"`
}

// SongEntry 歌单/拉黑/历史中的歌曲条目
type SongEntry struct {
	ID        int64      `json:"id"`
	MusicInfo music.Song `json:"musicInfo"`
	AddedAt   int64      `json:"addedAt"`
}

func (s *Store) CreatePlaylist(userID int64, name string, isDefault bool) (*Playlist, error) {
	res, err := s.db.Exec(`INSERT INTO playlists(user_id, name, is_default, created_at) VALUES(?,?,?,?)`,
		userID, name, boolInt(isDefault), time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Playlist{ID: id, Name: name, IsDefault: isDefault, CreatedAt: time.Now().Unix()}, nil
}

// EnsureDefaultPlaylist 保证默认歌单存在(登录时调用)
func (s *Store) EnsureDefaultPlaylist(userID int64) (*Playlist, error) {
	row := s.db.QueryRow(`SELECT id, name, is_default, created_at FROM playlists WHERE user_id=? AND is_default=1`, userID)
	p := &Playlist{}
	var d int
	if err := row.Scan(&p.ID, &p.Name, &d, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.CreatePlaylist(userID, "默认歌单", true)
		}
		return nil, err
	}
	p.IsDefault = d == 1
	return p, nil
}

func (s *Store) ListPlaylists(userID int64) ([]Playlist, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.name, p.is_default, p.created_at,
			(SELECT COUNT(*) FROM playlist_songs ps WHERE ps.playlist_id = p.id) AS cnt
		FROM playlists p WHERE p.user_id=? ORDER BY p.is_default DESC, p.id ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		var p Playlist
		var d int
		if err := rows.Scan(&p.ID, &p.Name, &d, &p.CreatedAt, &p.Count); err != nil {
			return nil, err
		}
		p.IsDefault = d == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) getPlaylist(id, userID int64) (*Playlist, error) {
	row := s.db.QueryRow(`SELECT id, name, is_default, created_at FROM playlists WHERE id=? AND user_id=?`, id, userID)
	p := &Playlist{}
	var d int
	if err := row.Scan(&p.ID, &p.Name, &d, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.IsDefault = d == 1
	return p, nil
}

func (s *Store) RenamePlaylist(id, userID int64, name string) error {
	p, err := s.getPlaylist(id, userID)
	if err != nil {
		return err
	}
	if p.IsDefault {
		return ErrDefaultPlaylist // 默认歌单名承担"收藏入口"语义,不允许重命名
	}
	_, err = s.db.Exec(`UPDATE playlists SET name=? WHERE id=?`, name, id)
	return err
}

func (s *Store) DeletePlaylist(id, userID int64) error {
	p, err := s.getPlaylist(id, userID)
	if err != nil {
		return err
	}
	if p.IsDefault {
		return ErrDefaultPlaylist
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM playlist_songs WHERE playlist_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM playlists WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListPlaylistSongs 歌单内歌曲,query 为空返回全部
// likeEscape 转义用户搜索词中的 LIKE 通配符(配合 ESCAPE '\' 使用)
func likeEscape(q string) string {
	var b strings.Builder
	for _, c := range q {
		if c == '%' || c == '_' || c == chrBackslash {
			b.WriteRune(chrBackslash)
		}
		b.WriteRune(c)
	}
	return "%" + b.String() + "%"
}

const chrBackslash rune = 92

func (s *Store) ListPlaylistSongs(playlistID, userID int64, query string) ([]SongEntry, error) {
	return s.PlaylistSongsPage(playlistID, userID, query, 0, 0)
}

func (s *Store) PlaylistSongsPage(playlistID, userID int64, query string, before int64, limit int) ([]SongEntry, error) {
	if _, err := s.getPlaylist(playlistID, userID); err != nil {
		return nil, err
	}
	sqlStr := `SELECT id, music_info, added_at FROM playlist_songs WHERE playlist_id=?`
	args := []any{playlistID}
	if q := strings.TrimSpace(query); q != "" {
		sqlStr += ` AND music_info LIKE ? ESCAPE '\'`
		args = append(args, likeEscape(q))
	}
	if before > 0 {
		sqlStr += ` AND id < ?`
		args = append(args, before)
	}
	sqlStr += ` ORDER BY id DESC`
	if limit > 0 {
		sqlStr += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.querySongs(sqlStr, args...)
}

// AddSongToPlaylist 收藏歌曲;已存在时静默成功。
// 同时回传该行 id:前端「取消收藏」可直接 DELETE 行 id,
// 不必先分页拉取整张歌单再删,取消收藏从两次请求降到一次。
func (s *Store) AddSongToPlaylist(playlistID, userID int64, song *music.Song) (bool, int64, error) {
	if _, err := s.getPlaylist(playlistID, userID); err != nil {
		return false, 0, err
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO playlist_songs(playlist_id, source, song_id, music_info, added_at) VALUES(?,?,?,?,?)`,
		playlistID, song.Source, songID(song), mustJSON(song), time.Now().Unix())
	if err != nil {
		return false, 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		id, _ := res.LastInsertId()
		return true, id, nil
	}
	// 已存在(重复收藏):回查行 id,失败不影响"收藏成功"语义
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM playlist_songs WHERE playlist_id=? AND source=? AND song_id=?`,
		playlistID, song.Source, songID(song)).Scan(&id); err != nil {
		return false, 0, nil
	}
	return false, id, nil
}

func (s *Store) RemovePlaylistSong(rowID, playlistID, userID int64) error {
	if _, err := s.getPlaylist(playlistID, userID); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM playlist_songs WHERE id=? AND playlist_id=?`, rowID, playlistID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // 行不存在/不属于该歌单:不要伪装成成功
	}
	return nil
}

/* ---------- blacklist ---------- */

func (s *Store) AddBlacklist(userID int64, song *music.Song) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO blacklist(user_id, source, song_id, music_info, created_at) VALUES(?,?,?,?,?)`,
		userID, song.Source, songID(song), mustJSON(song), time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddBlacklistAndClean 收藏联动拉黑:同一事务内加黑名单并移出所有收藏歌单。
// 回传 (是否新增, 行 id, 加入时间):前端据此本地插入黑名单条目,
// 省掉一次 GET /api/blacklist 全量刷新(最多 1000 行)。
func (s *Store) AddBlacklistAndClean(userID int64, song *music.Song) (bool, int64, int64, error) {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return false, 0, 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO blacklist(user_id, source, song_id, music_info, created_at) VALUES(?,?,?,?,?)`,
		userID, song.Source, songID(song), mustJSON(song), now)
	if err != nil {
		return false, 0, 0, err
	}
	n, _ := res.RowsAffected()
	newID, _ := res.LastInsertId() // 必须在 Commit 前取:提交后结果集已重置
	if _, err := tx.Exec(`DELETE FROM playlist_songs WHERE source=? AND song_id=? AND playlist_id IN
		(SELECT id FROM playlists WHERE user_id=?)`, song.Source, songID(song), userID); err != nil {
		return false, 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, 0, err
	}
	if n == 0 {
		// 已在黑名单:回查行 id,失败不影响"已拉黑"语义
		var id, created int64
		if err := s.db.QueryRow(`SELECT id, created_at FROM blacklist WHERE user_id=? AND source=? AND song_id=?`,
			userID, song.Source, songID(song)).Scan(&id, &created); err == nil {
			return false, id, created, nil
		}
		return false, 0, now, nil
	}
	return true, newID, now, nil
}

func (s *Store) RemoveBlacklist(rowID, userID int64) error {
	res, err := s.db.Exec(`DELETE FROM blacklist WHERE id=? AND user_id=?`, rowID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // 记录不存在(可能已被删除可重复点击):明确返回未找到
	}
	return nil
}

func (s *Store) ListBlacklist(userID int64, query string) ([]SongEntry, error) {
	return s.BlacklistPage(userID, query, 0, 0)
}

func (s *Store) BlacklistPage(userID int64, query string, before int64, limit int) ([]SongEntry, error) {
	sqlStr := `SELECT id, music_info, created_at FROM blacklist WHERE user_id=?`
	args := []any{userID}
	if q := strings.TrimSpace(query); q != "" {
		sqlStr += ` AND music_info LIKE ? ESCAPE '\'`
		args = append(args, likeEscape(q))
	}
	if before > 0 {
		sqlStr += ` AND id < ?`
		args = append(args, before)
	}
	sqlStr += ` ORDER BY id DESC`
	if limit > 0 {
		sqlStr += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.querySongs(sqlStr, args...)
}

/* ---------- history ---------- */

func (s *Store) AddHistory(userID int64, song *music.Song) error {
	_, err := s.RecordPlay(userID, song)
	return err
}

// RecordPlay commits the cumulative counter and the bounded recent history together.
func (s *Store) RecordPlay(userID int64, song *music.Song) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int64
	if err := tx.QueryRow(`UPDATE users SET listen_count=listen_count+1 WHERE id=? RETURNING listen_count`, userID).Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	// 同一首歌只保留最近一次播放
	if _, err := tx.Exec(`DELETE FROM play_history WHERE user_id=? AND source=? AND song_id=?`,
		userID, song.Source, songID(song)); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO play_history(user_id, source, song_id, name, singer, music_info, played_at) VALUES(?,?,?,?,?,?,?)`,
		userID, song.Source, songID(song), song.Name, song.Singer, mustJSON(song), time.Now().Unix()); err != nil {
		return 0, err
	}
	// 每用户仅保留最近 500 条(清理失败会使历史无限增长,需检查)
	if _, err := tx.Exec(`DELETE FROM play_history WHERE user_id=? AND id NOT IN (
		SELECT id FROM play_history WHERE user_id=? ORDER BY id DESC LIMIT 500)`, userID, userID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListHistory(userID int64, query string, limit int) ([]SongEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sqlStr := `SELECT id, music_info, played_at FROM play_history WHERE user_id=?`
	args := []any{userID}
	if q := strings.TrimSpace(query); q != "" {
		sqlStr += ` AND (name LIKE ? ESCAPE '\' OR singer LIKE ? ESCAPE '\')`
		args = append(args, likeEscape(q), likeEscape(q))
	}
	sqlStr += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return s.querySongs(sqlStr, args...)
}

func (s *Store) ClearHistory(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM play_history WHERE user_id=?`, userID)
	return err
}

/* ---------- helpers ---------- */

func (s *Store) querySongs(sqlStr string, args ...any) ([]SongEntry, error) {
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SongEntry{}
	for rows.Next() {
		var e SongEntry
		var info string
		if err := rows.Scan(&e.ID, &info, &e.AddedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(info), &e.MusicInfo) == nil {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

func songID(s *music.Song) string {
	id := s.Songmid
	if id == "" && s.Hash != "" {
		id = s.Hash
	}
	if id == "" && s.CopyrightID != "" {
		id = s.CopyrightID
	}
	return id
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
