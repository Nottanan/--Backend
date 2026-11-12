package auth

import "testing"

func TestHashPassword(t *testing.T) {
	password := "secret123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}

	if hash == password {
		t.Fatal("password must not be stored as plain text")
	}

	if !CheckPassword(hash, password) {
		t.Fatal("correct password should pass")
	}

	if CheckPassword(hash, "wrong-password") {
		t.Fatal("wrong password should fail")
	}
}

func TestCheckPostgreSQLHash(t *testing.T) {
	hash := "$2a$10$/gPnoRYpGhY/hECaM17lCeQR/74.FWofAPeh8Pl7DfAoQNvUcXYfK"

	if !CheckPassword(hash, "krua-aroi-test-2026") {
		t.Fatal("PostgreSQL bcrypt hash should pass")
	}
}
