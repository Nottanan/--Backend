package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	authpkg "foodorder/auth"
	"foodorder/mw"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (cr *credentials) validate() error {
	cr.Username = strings.TrimSpace(cr.Username)

	if n := utf8.RuneCountInString(cr.Username); n < 3 || n > 30 {
		return errors.New("ชื่อผู้ใช้ต้องยาว 3-30 ตัวอักษร")
	}

	if len(cr.Password) < 6 || len(cr.Password) > 72 {
		return errors.New("รหัสผ่านต้องยาว 6-72 ตัวอักษร")
	}

	return nil
}

func register(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, issuer *authpkg.Issuer) {
	var cr credentials

	if err := json.NewDecoder(r.Body).Decode(&cr); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	if err := cr.validate(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := authpkg.HashPassword(cr.Password)
	if err != nil {
		serverError(w, err)
		return
	}

	var id int64
	err = db.QueryRow(r.Context(), `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, 'customer')
		ON CONFLICT (username) DO NOTHING
		RETURNING id
	`, cr.Username, hash).Scan(&id)

	if err == pgx.ErrNoRows {
		fail(w, http.StatusConflict, "ชื่อผู้ใช้นี้ถูกใช้แล้ว")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	token, err := issuer.Issue(id, "customer")
	if err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": cr.Username,
		"role":     "customer",
	})
}

func login(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, issuer *authpkg.Issuer) {
	var cr credentials

	if err := json.NewDecoder(r.Body).Decode(&cr); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	cr.Username = strings.TrimSpace(cr.Username)

	var id int64
	var passwordHash string
	var role string

	err := db.QueryRow(r.Context(), `
		SELECT id, password_hash, role
		FROM users
		WHERE username = $1
	`, cr.Username).Scan(&id, &passwordHash, &role)

	if err == pgx.ErrNoRows || (err == nil && !authpkg.CheckPassword(passwordHash, cr.Password)) {
		fail(w, http.StatusUnauthorized, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}

	token, err := issuer.Issue(id, role)
	if err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": cr.Username,
		"role":     role,
	})
}

func logout(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	w.WriteHeader(http.StatusNoContent)
}

func getUserID(r *http.Request) int {
	id, ok := mw.MemberID(r.Context())
	if !ok {
		return 0
	}
	return int(id)
}

func getRole(r *http.Request) string {
	return mw.Role(r.Context())
}
