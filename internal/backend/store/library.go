package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrAlreadyInLibrary = errors.New("already in library")
	ErrNotInLibrary     = errors.New("not in library")
)

type LibraryItem struct {
	UserID, MangaID, Status string
	CurrentChapter, Version int
	UpdatedAt               time.Time
}

const libCols = `user_id, manga_id, status, current_chapter, updated_at, version`

func scanLib(r interface{ Scan(...any) error }) (LibraryItem, error) {
	var it LibraryItem
	err := r.Scan(&it.UserID, &it.MangaID, &it.Status, &it.CurrentChapter, &it.UpdatedAt, &it.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryItem{}, ErrNotInLibrary
	}
	return it, err
}

// AddLibrary relies on the (user_id, manga_id) primary key to reject duplicates, not a read-then-insert.
func (s *Store) AddLibrary(ctx context.Context, userID, mangaID, status string, chapter int) (LibraryItem, error) {
	it, err := scanLib(s.db.QueryRowContext(ctx,
		`INSERT INTO user_library (user_id, manga_id, status, current_chapter) VALUES (?, ?, ?, ?) RETURNING `+libCols,
		userID, mangaID, status, chapter))
	var se sqlite3.Error
	if errors.As(err, &se) && (se.ExtendedCode == sqlite3.ErrConstraintPrimaryKey || se.ExtendedCode == sqlite3.ErrConstraintUnique) {
		return LibraryItem{}, ErrAlreadyInLibrary
	}
	return it, err
}

// ListLibrary returns only userID's rows, newest first; never nil.
func (s *Store) ListLibrary(ctx context.Context, userID string) ([]LibraryItem, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+libCols+` FROM user_library WHERE user_id = ? ORDER BY updated_at DESC, manga_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []LibraryItem{}
	for rows.Next() {
		it, err := scanLib(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) GetLibrary(ctx context.Context, userID, mangaID string) (LibraryItem, error) {
	return scanLib(s.db.QueryRowContext(ctx,
		`SELECT `+libCols+` FROM user_library WHERE user_id = ? AND manga_id = ?`, userID, mangaID))
}

// UpdateProgress is one atomic statement: nil arguments keep the column, and
// version is bumped in the same UPDATE, so concurrent updates never share a version.
// The statement autocommits, so the row is durable when this returns.
func (s *Store) UpdateProgress(ctx context.Context, userID, mangaID string, chapter *int, status *string) (LibraryItem, error) {
	var ch sql.NullInt64
	if chapter != nil {
		ch = sql.NullInt64{Int64: int64(*chapter), Valid: true}
	}
	var st sql.NullString
	if status != nil {
		st = sql.NullString{String: *status, Valid: true}
	}
	return scanLib(s.db.QueryRowContext(ctx, `UPDATE user_library
SET current_chapter = COALESCE(?, current_chapter),
    status          = COALESCE(?, status),
    updated_at      = CURRENT_TIMESTAMP,
    version         = version + 1
WHERE user_id = ? AND manga_id = ?
RETURNING `+libCols, ch, st, userID, mangaID))
}
