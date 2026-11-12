// Package data embeds the catalog seed file so the catalog needs no path to start.
// The seed JSON may carry extra fields (artist, cover_url, year, rating); the
// importer ignores them.
package data

import _ "embed"

//go:embed manga_seeds.json
var MangaSeeds []byte
