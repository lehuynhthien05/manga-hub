package auth

// Verifier checks a JWT and returns its claims. The HTTP middleware and the
// TCP, UDP and WebSocket servers depend on this interface, not on a secret.
type Verifier interface {
	Verify(token string) (Claims, error)
}
