package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"mangahub/pkg/models"
)

// InitDB initializes the SQLite database and ensures schemas and seed data exist
func InitDB(dbPath string) (*sql.DB, error) {
	resolvedPath, err := ResolvePath(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolving db path: %w", err)
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite3", resolvedPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	// Connection pooling configuration (UC-029)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(10 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging sqlite database: %w", err)
	}

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("creating tables: %w", err)
	}

	// Seed database if manga count is 0
	if err := seedMangaIfEmpty(db); err != nil {
		// Log warning but don't fail hard
		fmt.Printf("Warning: Seeding manga: %v\n", err)
	}

	return db, nil
}

func ResolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Clean(path), nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT UNIQUE NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS manga (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		author TEXT,
		artist TEXT,
		genres TEXT, -- JSON array string
		status TEXT,
		total_chapters INTEGER DEFAULT 0,
		description TEXT,
		cover_url TEXT,
		year INTEGER,
		rating REAL DEFAULT 0.0
	);

	CREATE INDEX IF NOT EXISTS idx_manga_title ON manga(title);
	CREATE INDEX IF NOT EXISTS idx_manga_author ON manga(author);
	CREATE INDEX IF NOT EXISTS idx_manga_status ON manga(status);

	CREATE TABLE IF NOT EXISTS user_progress (
		user_id TEXT NOT NULL,
		manga_id TEXT NOT NULL,
		current_chapter INTEGER DEFAULT 0,
		status TEXT DEFAULT 'plan_to_read',
		rating INTEGER DEFAULT 0,
		notes TEXT DEFAULT '',
		started_at TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, manga_id),
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (manga_id) REFERENCES manga(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_progress_user ON user_progress(user_id);
	CREATE INDEX IF NOT EXISTS idx_progress_status ON user_progress(user_id, status);

	CREATE TABLE IF NOT EXISTS reviews (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		username TEXT NOT NULL,
		manga_id TEXT NOT NULL,
		rating INTEGER NOT NULL,
		text TEXT,
		timestamp INTEGER NOT NULL,
		helpful INTEGER DEFAULT 0,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
		FOREIGN KEY (manga_id) REFERENCES manga(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_reviews_manga ON reviews(manga_id);

	CREATE TABLE IF NOT EXISTS chat_messages (
		id TEXT PRIMARY KEY,
		room TEXT NOT NULL,
		user_id TEXT NOT NULL,
		username TEXT NOT NULL,
		message TEXT NOT NULL,
		timestamp INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_chat_room ON chat_messages(room, timestamp);

	CREATE TABLE IF NOT EXISTS friends (
		user_id TEXT NOT NULL,
		friend_id TEXT NOT NULL,
		status TEXT DEFAULT 'accepted',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, friend_id)
	);
	`
	_, err := db.Exec(schema)
	return err
}

func seedMangaIfEmpty(db *sql.DB) error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM manga").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	// Try loading data/manga_seeds.json
	seedPaths := []string{
		"data/manga_seeds.json",
		"../data/manga_seeds.json",
		"../../data/manga_seeds.json",
	}

	var data []byte
	for _, p := range seedPaths {
		if content, readErr := os.ReadFile(p); readErr == nil {
			data = content
			break
		}
	}

	if len(data) == 0 {
		return nil
	}

	var mangas []models.Manga
	if err := json.Unmarshal(data, &mangas); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO manga (id, title, author, artist, genres, status, total_chapters, description, cover_url, year, rating)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, m := range mangas {
		genresJSON, _ := json.Marshal(m.Genres)
		_, err := stmt.Exec(m.ID, m.Title, m.Author, m.Artist, string(genresJSON), m.Status, m.TotalChapters, m.Description, m.CoverURL, m.Year, m.Rating)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// DB Check
func CheckDB(db *sql.DB) (map[string]int, error) {
	counts := make(map[string]int)
	tables := []string{"users", "manga", "user_progress", "reviews", "chat_messages"}
	for _, table := range tables {
		var cnt int
		row := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
		if err := row.Scan(&cnt); err == nil {
			counts[table] = cnt
		}
	}
	return counts, nil
}

// DB Optimize
func OptimizeDB(db *sql.DB) error {
	_, err := db.Exec("VACUUM; ANALYZE; REINDEX;")
	return err
}

// RepairDB checks integrity and cleans up any orphaned progress rows
func RepairDB(db *sql.DB) (int, error) {
	// Delete user_progress where user_id or manga_id doesn't exist
	res, err := db.Exec(`
		DELETE FROM user_progress
		WHERE user_id NOT IN (SELECT id FROM users)
		   OR manga_id NOT IN (SELECT id FROM manga)
	`)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	_ = OptimizeDB(db)
	return int(affected), nil
}
