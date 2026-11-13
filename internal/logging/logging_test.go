package logging_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"mangahub/internal/logging"
)

func TestNewWriter_ComponentAndLevel(t *testing.T) {
	var buf bytes.Buffer
	log := logging.NewWriter(&buf, "tcp", "warn")
	log.Info("hidden")
	log.Warn("shown", "addr", ":9090")
	out := buf.String()
	assert.NotContains(t, out, "hidden")
	assert.Contains(t, out, "component=tcp")
	assert.Contains(t, out, "shown")
}

func TestNewWriter_UnknownLevelFallsBackToInfo(t *testing.T) {
	var buf bytes.Buffer
	logging.NewWriter(&buf, "x", "loud").Info("hello")
	assert.Contains(t, buf.String(), "hello")
}

// Guard for the rule in AGENTS.md: the logger helper adds no secret fields.
func TestNewWriter_AddsNoSecretFields(t *testing.T) {
	var buf bytes.Buffer
	logging.NewWriter(&buf, "auth", "debug").Info("login", "user", "alice")
	out := buf.String()
	assert.NotContains(t, out, "token")
	assert.NotContains(t, out, "password")
}
