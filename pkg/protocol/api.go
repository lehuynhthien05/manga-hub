package protocol

import "time"

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

type Manga struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Author        string   `json:"author"`
	Genres        []string `json:"genres"`
	Status        string   `json:"status"`
	TotalChapters int      `json:"total_chapters"`
	Description   string   `json:"description"`
}

type SearchResponse struct {
	Items []Manga `json:"items"`
	Total int     `json:"total"`
	Page  int     `json:"page"`
	Limit int     `json:"limit"`
}

type LibraryItem struct {
	MangaID        string    `json:"manga_id"`
	Status         string    `json:"status"`
	CurrentChapter int       `json:"current_chapter"`
	Version        int       `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Manga is filled from the catalog; nil when the catalog is unavailable.
	Manga *Manga `json:"manga,omitempty"`
}

// Values for LibraryResponse.Catalog.
const (
	CatalogOK          = "ok"
	CatalogUnavailable = "unavailable"
)

type LibraryResponse struct {
	Items   []LibraryItem `json:"items"`
	Catalog string        `json:"catalog"`
}

type AddLibraryRequest struct {
	MangaID string `json:"manga_id"`
	Status  string `json:"status"`
}

// Pointers tell "missing" from "0": chapter 0 is a valid value.
type UpdateProgressRequest struct {
	CurrentChapter *int    `json:"current_chapter"`
	Status         *string `json:"status"`
}

type ReleaseRequest struct {
	Chapter *int `json:"chapter"`
}

// UDPSent counts datagrams *attempted*, never "delivered".
type ReleaseResponse struct {
	Manga   Manga `json:"manga"`
	Chapter int   `json:"chapter"`
	UDPSent int   `json:"udp_sent"`
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Values for ErrorResponse.Code.
const (
	CodeInvalidInput       = "invalid_input"
	CodeUnauthenticated    = "unauthenticated"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeInternal           = "internal"
	CodeBadGateway         = "bad_gateway"
	CodeCatalogUnavailable = "catalog_unavailable"
)
