package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrUsernameTaken = errors.New("username already taken")
	ErrUserNotFound  = errors.New("user not found")
)

type User struct {
	ID, Username, PasswordHash, Role string
	CreatedAt                        time.Time
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) CreateUser(ctx context.Context, username, hash, role string) (User, error) {
	u := User{ID: newID("usr_"), Username: username, PasswordHash: hash, Role: role}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO users(id, username, password_hash, role) VALUES (?,?,?,?) RETURNING created_at`,
		u.ID, u.Username, u.PasswordHash, u.Role).Scan(&u.CreatedAt)
	var se sqlite3.Error
	if errors.As(err, &se) && se.ExtendedCode == sqlite3.ErrConstraintUnique {
		return User{}, ErrUsernameTaken
	}
	return u, err
}

// GetByUsername is case-insensitive because the column is COLLATE NOCASE.
func (s *Store) GetByUsername(ctx context.Context, username string) (User, error) {
	return s.getUser(ctx, `WHERE username = ?`, username)
}

func (s *Store) GetByID(ctx context.Context, id string) (User, error) {
	return s.getUser(ctx, `WHERE id = ?`, id)
}

func (s *Store) getUser(ctx context.Context, where string, arg string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, role, created_at FROM users `+where, arg).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return u, err
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on supported platforms
	return prefix + hex.EncodeToString(b)
}
