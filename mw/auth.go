package mw

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"foodorder/auth"
)

type ctxKey string

const (
	memberIDKey ctxKey = "uid"
	roleKey     ctxKey = "role"
)

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
	})
}

func RequireLogin(issuer *auth.Issuer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")

		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "กรุณาเข้าสู่ระบบ")
			return
		}

		tokenString := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if tokenString == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "กรุณาเข้าสู่ระบบ")
			return
		}

		id, role, err := issuer.Parse(tokenString)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "กรุณาเข้าสู่ระบบ")
			return
		}

		ctx := context.WithValue(r.Context(), memberIDKey, id)
		ctx = context.WithValue(ctx, roleKey, role)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Role(r.Context()) != role {
			writeError(w, http.StatusForbidden, "forbidden", "ไม่มีสิทธิ์เข้าถึง")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func MemberID(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(memberIDKey).(int64)
	return id, ok
}

func Role(ctx context.Context) string {
	role, _ := ctx.Value(roleKey).(string)
	return role
}
