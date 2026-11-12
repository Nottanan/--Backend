package auth

import (
	"strings"
	"testing"
	"time"
)

func TestIssueAndParseToken(t *testing.T) {
	issuer := &Issuer{
		Secret: []byte("test-secret-at-least-32-characters"),
		TTL:    time.Hour,
	}

	token, err := issuer.Issue(123, "admin")
	if err != nil {
		t.Fatal(err)
	}

	id, role, err := issuer.Parse(token)
	if err != nil {
		t.Fatal(err)
	}

	if id != 123 {
		t.Fatalf("want ID 123, got %d", id)
	}

	if role != "admin" {
		t.Fatalf("want role admin, got %s", role)
	}
}

func TestParseInvalidToken(t *testing.T) {
	issuer := &Issuer{
		Secret: []byte("test-secret-at-least-32-characters"),
		TTL:    time.Hour,
	}

	token, err := issuer.Issue(123, "customer")
	if err != nil {
		t.Fatal(err)
	}

	tampered := token[:len(token)-1] + "x"

	if _, _, err := issuer.Parse(tampered); err == nil {
		t.Fatal("tampered token should be rejected")
	}

	if _, _, err := issuer.Parse("invalid-token"); err == nil {
		t.Fatal("invalid token should be rejected")
	}
}

func TestParseExpiredToken(t *testing.T) {
	issuer := &Issuer{
		Secret: []byte("test-secret-at-least-32-characters"),
		TTL:    -time.Hour,
	}

	token, err := issuer.Issue(123, "customer")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := issuer.Parse(token); err == nil {
		t.Fatal("expired token should be rejected")
	}
}

func TestParseTokenWithWrongSecret(t *testing.T) {
	issuer := &Issuer{
		Secret: []byte("test-secret-at-least-32-characters"),
		TTL:    time.Hour,
	}

	token, err := issuer.Issue(123, "customer")
	if err != nil {
		t.Fatal(err)
	}

	wrongIssuer := &Issuer{
		Secret: []byte(strings.Repeat("x", 32)),
		TTL:    time.Hour,
	}

	if _, _, err := wrongIssuer.Parse(token); err == nil {
		t.Fatal("token signed with another secret should be rejected")
	}
}
