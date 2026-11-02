package store

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeCannotOverwriteConcurrentReset(t *testing.T) {
	s, u := securityStore(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if err := s.UpdatePasswordHash(u.ID, string(hash)); err != nil {
		t.Fatal(err)
	}
	verified, err := s.VerifiedPasswordHash(u.ID, "old-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetUserPassword(u.ID, "admin-new-hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSessionChecked("new-session", u.ID, time.Hour, "admin-new-hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePasswordHash(u.ID, "stale-change", verified); !errors.Is(err, ErrPasswordChanged) {
		t.Fatalf("stale change: %v", err)
	}
	if _, _, err := s.SessionUser("new-session"); err != nil {
		t.Fatalf("failed change revoked current session: %v", err)
	}
	if err := s.CreateSessionChecked("attacker", u.ID, time.Hour, "stale-change"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale hash accepted: %v", err)
	}
}

func TestPasswordChangeCommitsVerifiedVersionAndRevokesSessions(t *testing.T) {
	s, u := securityStore(t)
	oldHash, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	newHash, _ := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)
	if err := s.UpdatePasswordHash(u.ID, string(oldHash)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSessionChecked("old-session", u.ID, time.Hour, string(oldHash)); err != nil {
		t.Fatal(err)
	}
	verified, err := s.VerifiedPasswordHash(u.ID, "old-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePasswordHash(u.ID, string(newHash), verified); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPassword(u.ID, "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPassword(u.ID, "old-password"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("old password: %v", err)
	}
	if _, _, err := s.SessionUser("old-session"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old session: %v", err)
	}
}
