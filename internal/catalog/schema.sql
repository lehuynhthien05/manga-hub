-- Catalog database (var/catalog.db): manga metadata, served over gRPC.
CREATE TABLE IF NOT EXISTS manga (
  id             TEXT PRIMARY KEY,
  title          TEXT NOT NULL,
  author         TEXT NOT NULL DEFAULT '',
  genres         TEXT NOT NULL DEFAULT '[]',  -- JSON array, filtered with json_each()
  status         TEXT NOT NULL DEFAULT 'ongoing' CHECK (status IN ('ongoing','completed','hiatus')),
  total_chapters INTEGER NOT NULL DEFAULT 0 CHECK (total_chapters >= 0),
  description    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_manga_title ON manga(title COLLATE NOCASE);
