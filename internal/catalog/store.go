package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound      = errors.New("manga not found")
	ErrNotIncreasing = errors.New("new total is not greater than the current total")
	ErrInvalid       = errors.New("invalid argument")
)

type Manga struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Author        string   `json:"author"`
	Genres        []string `json:"genres"`
	Status        string   `json:"status"`
	TotalChapters int      `json:"total_chapters"`
	Description   string   `json:"description"`
}

type SearchParams struct {
	Query, Genre, Status string
	Limit, Offset        int
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const cols = `id, title, author, genres, status, total_chapters, description`

// SeedIfEmpty imports the seed JSON in one transaction, only when the table is empty.
// Unknown JSON fields (artist, cover_url, year, rating) are ignored.
func (s *Store) SeedIfEmpty(ctx context.Context, raw []byte) (int, error) {
	var recs []Manga
	if err := json.Unmarshal(raw, &recs); err != nil {
		return 0, fmt.Errorf("parse seed: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM manga`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	for _, m := range recs {
		genres, _ := json.Marshal(m.Genres)
		if _, err := tx.ExecContext(ctx, `INSERT INTO manga(`+cols+`) VALUES (?,?,?,?,?,?,?)`,
			m.ID, m.Title, m.Author, string(genres), m.Status, m.TotalChapters, m.Description); err != nil {
			return 0, fmt.Errorf("seed %q: %w", m.ID, err)
		}
	}
	return len(recs), tx.Commit()
}

type scanner interface{ Scan(...any) error }

func scanManga(r scanner) (Manga, error) {
	var m Manga
	var genres string
	if err := r.Scan(&m.ID, &m.Title, &m.Author, &genres, &m.Status, &m.TotalChapters, &m.Description); err != nil {
		return m, err
	}
	return m, json.Unmarshal([]byte(genres), &m.Genres)
}

func (s *Store) Get(ctx context.Context, id string) (Manga, error) {
	return get(ctx, s.db, id)
}

func get(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (Manga, error) {
	m, err := scanManga(q.QueryRowContext(ctx, `SELECT `+cols+` FROM manga WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Search returns one page plus the total match count. limit<=0 means 20, capped at 100.
func (s *Store) Search(ctx context.Context, p SearchParams) ([]Manga, int, error) {
	if p.Limit < 0 || p.Offset < 0 {
		return nil, 0, ErrInvalid
	}
	limit := p.Limit
	if limit == 0 {
		limit = 20
	}
	limit = min(limit, 100)

	// Dynamic SQL is built only from the constant fragments below; user values are bound.
	where, args := []string{"1=1"}, []any{}
	if p.Query != "" {
		like := "%" + escapeLike(p.Query) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR author LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	if p.Genre != "" {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(manga.genres) WHERE json_each.value = ? COLLATE NOCASE)`)
		args = append(args, p.Genre)
	}
	if p.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, p.Status)
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM manga WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM manga WHERE `+cond+
		` ORDER BY title COLLATE NOCASE LIMIT ? OFFSET ?`, append(args, limit, p.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Manga{}
	for rows.Next() {
		m, err := scanManga(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}

// UpdateChapters raises total_chapters and returns the previous value.
// The DSN's _txlock=immediate makes concurrent callers queue instead of racing.
func (s *Store) UpdateChapters(ctx context.Context, id string, newTotal int) (Manga, int, error) {
	if id == "" || newTotal <= 0 {
		return Manga{}, 0, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Manga{}, 0, err
	}
	defer tx.Rollback()
	var prev int
	err = tx.QueryRowContext(ctx, `SELECT total_chapters FROM manga WHERE id = ?`, id).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return Manga{}, 0, ErrNotFound
	}
	if err != nil {
		return Manga{}, 0, err
	}
	if newTotal <= prev {
		return Manga{}, prev, ErrNotIncreasing
	}
	if _, err := tx.ExecContext(ctx, `UPDATE manga SET total_chapters = ? WHERE id = ?`, newTotal, id); err != nil {
		return Manga{}, 0, err
	}
	m, err := get(ctx, tx, id)
	if err != nil {
		return Manga{}, 0, err
	}
	return m, prev, tx.Commit()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
