package data_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/data"
)

// v3.0 "minimum dataset": fails loudly if the seed file is edited badly.
func TestSeeds_MinimumDataset(t *testing.T) {
	var seeds []struct {
		ID            string   `json:"id"`
		Title         string   `json:"title"`
		Author        string   `json:"author"`
		Genres        []string `json:"genres"`
		Status        string   `json:"status"`
		TotalChapters int      `json:"total_chapters"`
		Description   string   `json:"description"`
	}
	require.NoError(t, json.Unmarshal(data.MangaSeeds, &seeds))
	require.GreaterOrEqual(t, len(seeds), 30)

	slug := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	ids := map[string]bool{}
	genres := map[string]bool{}
	for _, s := range seeds {
		assert.Regexp(t, slug, s.ID)
		assert.False(t, ids[s.ID], "duplicate id %q", s.ID)
		ids[s.ID] = true
		assert.NotEmpty(t, s.Title, s.ID)
		assert.NotEmpty(t, s.Author, s.ID)
		assert.NotEmpty(t, s.Description, s.ID)
		assert.Contains(t, []string{"ongoing", "completed", "hiatus"}, s.Status, s.ID)
		assert.GreaterOrEqual(t, s.TotalChapters, 0, s.ID)
		for _, g := range s.Genres {
			genres[g] = true
		}
	}
	assert.GreaterOrEqual(t, len(genres), 4)
}
