package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/pkg/protocol"
)

// Golden examples from v3.0 §4.
func TestGolden_V3Examples(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		v    any
	}{
		{"progress_updated", `{"type":"progress_updated","manga_id":"one-piece","chapter":25,"version":2}`,
			&protocol.ProgressUpdated{}},
		{"new_chapter", `{"type":"new_chapter","manga_id":"one-piece","chapter":26}`, &protocol.NewChapter{}},
		{"chat", `{"type":"chat","room":"general","text":"Chapter 26 is out!"}`, &protocol.WSChat{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, json.Unmarshal([]byte(tc.raw), tc.v))
			out, err := json.Marshal(tc.v)
			require.NoError(t, err)
			assert.JSONEq(t, tc.raw, string(out))
		})
	}
}

func TestChapterZeroIsSerialised(t *testing.T) {
	b, err := json.Marshal(protocol.ProgressUpdated{Type: protocol.TypeProgressUpdated, MangaID: "x", Chapter: 0, Version: 1})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"chapter":0`)
	b, err = json.Marshal(protocol.NewChapter{Type: protocol.TypeNewChapter, MangaID: "x"})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"chapter":0`)
}

func TestPeekType(t *testing.T) {
	typ, err := protocol.PeekType([]byte(`{"type":"auth","token":"t"}`))
	require.NoError(t, err)
	assert.Equal(t, protocol.TypeAuth, typ)

	for _, bad := range []string{`not json`, `{}`, `{"type":5}`} {
		_, err := protocol.PeekType([]byte(bad))
		assert.Error(t, err, bad)
	}
}

func TestEncodeLine(t *testing.T) {
	b, err := protocol.EncodeLine(protocol.TCPPong{Type: protocol.TypePong})
	require.NoError(t, err)
	assert.Equal(t, "{\"type\":\"pong\"}\n", string(b))
}

func TestValidStatus(t *testing.T) {
	for _, s := range []string{"plan-to-read", "reading", "completed"} {
		assert.True(t, protocol.ValidStatus(s), s)
	}
	for _, s := range []string{"", "plan_to_read", "on-hold", "dropped"} {
		assert.False(t, protocol.ValidStatus(s), s)
	}
}

func TestValidRoom(t *testing.T) {
	assert.True(t, protocol.ValidRoom("general"))
	assert.True(t, protocol.ValidRoom("one_piece-2"))
	for _, s := range []string{"", "General", "a b", "../x", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		assert.False(t, protocol.ValidRoom(s), s)
	}
}

func TestValidateUsernameAndPassword(t *testing.T) {
	assert.NoError(t, protocol.ValidateUsername("alice_1"))
	assert.Error(t, protocol.ValidateUsername("ab"))
	assert.Error(t, protocol.ValidateUsername("has space"))
	assert.NoError(t, protocol.ValidatePassword("Passw0rdX"))
	assert.Error(t, protocol.ValidatePassword("short1A"))
	assert.Error(t, protocol.ValidatePassword("alllowercase1"))
}
