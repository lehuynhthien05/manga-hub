-- Backend database (var/backend.db): accounts and each user's library.
CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS user_library (
  user_id         TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  manga_id        TEXT    NOT NULL,          -- owned by the catalog DB, so no foreign key
  status          TEXT    NOT NULL CHECK (status IN ('plan-to-read','reading','completed')),
  current_chapter INTEGER NOT NULL DEFAULT 0 CHECK (current_chapter >= 0),
  updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  version         INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (user_id, manga_id)            -- no duplicate entries; also serves lookups by user_id
);
