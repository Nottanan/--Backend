
package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookie = "session"
	sessionDays   = 7
)

type contextKey string

const (
	userIDKey   contextKey = "uid"
	usernameKey contextKey = "username"
	roleKey     contextKey = "role"
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

// register creates a customer account.
func register(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var cr credentials

	if err := decodeJSON(r, &cr); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	if err := cr.validate(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	var id int

	err := db.QueryRow(r.Context(), `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, crypt($2, gen_salt('bf', 10)), 'customer')
		ON CONFLICT (username) DO NOTHING
		RETURNING id
	`, cr.Username, cr.Password).Scan(&id)

	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusConflict, "ชื่อผู้ใช้นี้ถูกใช้แล้ว")
		return
	}

	if err != nil {
		serverError(w, err)
		return
	}

	startSession(w, r, db, id, cr.Username, "customer")
}

// login verifies username and password.
func login(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var cr credentials

	if err := decodeJSON(r, &cr); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	cr.Username = strings.TrimSpace(cr.Username)

	var id int
	var role string

	err := db.QueryRow(r.Context(), `
		SELECT id, role
		FROM users
		WHERE username = $1
		AND password_hash = crypt($2, password_hash)
	`, cr.Username, cr.Password).Scan(&id, &role)

	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusUnauthorized, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	if err != nil {
		serverError(w, err)
		return
	}

	startSession(w, r, db, id, cr.Username, role)
}

// startSession creates a session token and sends it as a cookie.
func startSession(
	w http.ResponseWriter,
	r *http.Request,
	db *pgxpool.Pool,
	id int,
	username string,
	role string,
) {
	var token string

	err := db.QueryRow(r.Context(), `
		INSERT INTO sessions (token, user_id)
		VALUES (encode(gen_random_bytes(32), 'hex'), $1)
		RETURNING token
	`, id).Scan(&token)

	if err != nil {
		serverError(w, err)
		return
	}

	setCookie(w, r, token, sessionDays*24*60*60)

	writeJSON(w, http.StatusOK, map[string]string{
		"username": username,
		"role":     role,
	})
}

// logout deletes the current session.
func logout(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_, err := db.Exec(r.Context(),
			`DELETE FROM sessions WHERE token = $1`,
			cookie.Value,
		)

		if err != nil {
			serverError(w, err)
			return
		}
	}

	setCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// setCookie configures the session cookie.
func setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
        secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

        sameSite := http.SameSiteLaxMode
        if secure {
                sameSite = http.SameSiteNoneMode
        }

        http.SetCookie(w, &http.Cookie{
                Name:     sessionCookie,
                Value:    token,
                Path:     "/",
                MaxAge:   maxAge,
                HttpOnly: true,
                Secure:   secure,
                SameSite: sameSite,
        })
}

// auth checks whether the user has a valid session and required role.
func auth(db *pgxpool.Pool, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookie)

			if err != nil {
				fail(w, http.StatusUnauthorized, "กรุณาเข้าสู่ระบบ")
				return
			}

			var id int
			var username string
			var role string

			err = db.QueryRow(r.Context(), `
				SELECT u.id, u.username, u.role
				FROM sessions s
				JOIN users u ON u.id = s.user_id
				WHERE s.token = $1
				AND s.created_at > now() - make_interval(days => $2)
			`, cookie.Value, sessionDays).Scan(&id, &username, &role)

			if errors.Is(err, pgx.ErrNoRows) {
				fail(w, http.StatusUnauthorized, "กรุณาเข้าสู่ระบบ")
				return
			}

			if err != nil {
				serverError(w, err)
				return
			}

			if len(roles) > 0 && !slices.Contains(roles, role) {
				fail(w, http.StatusForbidden, "ไม่มีสิทธิ์เข้าถึง")
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, id)
			ctx = context.WithValue(ctx, usernameKey, username)
			ctx = context.WithValue(ctx, roleKey, role)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Helper functions to read user information from request context.

func getUserID(r *http.Request) int {
	id, _ := r.Context().Value(userIDKey).(int)
	return id
}

func getUsername(r *http.Request) string {
	username, _ := r.Context().Value(usernameKey).(string)
	return username
}

func getRole(r *http.Request) string {
	role, _ := r.Context().Value(roleKey).(string)
	return role
}

// Prevent an unused import if session-related time logic is extended later.
var _ = time.Second
