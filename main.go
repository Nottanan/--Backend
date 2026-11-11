package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type orderReq struct {
	Items []struct {
		MenuID  int      `json:"menu_id"`
		Qty     int      `json:"qty"`
		Options []string `json:"options"`
	} `json:"items"`
}

func (r *orderReq) validate() error {
	if len(r.Items) == 0 {
		return errors.New("ตะกร้ายังว่างอยู่")
	}

	if len(r.Items) > 50 {
		return errors.New("สั่งได้ไม่เกิน 50 รายการต่อออเดอร์")
	}

	for _, it := range r.Items {
		if it.Qty < 1 || it.Qty > 99 {
			return errors.New("จำนวนต้องอยู่ระหว่าง 1-99")
		}
	}

	return nil
}

type optionGroup struct {
	Name    string   `json:"name"`
	Choices []string `json:"choices"`
}

type menuReq struct {
	Name    string        `json:"name"`
	Price   int           `json:"price"`
	Emoji   string        `json:"emoji"`
	Image   string        `json:"image"`
	Options []optionGroup `json:"options"`
}

type menuUpdateReq struct {
	Name        string        `json:"name"`
	Price       int           `json:"price"`
	Emoji       string        `json:"emoji"`
	Image       string        `json:"image"`
	Options     []optionGroup `json:"options"`
	IsAvailable bool          `json:"is_available"`
}

// pickOptions checks the chosen choices belong to this dish.
func pickOptions(groups []optionGroup, chosen []string) ([]string, error) {
	out := []string{}

	for _, g := range groups {
		pick := ""

		for _, c := range chosen {
			if contains(g.Choices, c) {
				if pick != "" {
					return nil, errors.New("เลือก" + g.Name + "ได้แค่อย่างเดียว")
				}

				pick = c
			}
		}

		if pick != "" {
			out = append(out, pick)
		}
	}

	if len(out) != len(chosen) {
		return nil, errors.New("ตัวเลือกไม่ถูกต้องสำหรับเมนูนี้")
	}

	return out, nil
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}

	return false
}

var statuses = map[string]bool{
	"pending":   true,
	"cooking":   true,
	"ready":     true,
	"done":      true,
	"cancelled": true,
}

// ordersSQL builds the order list as JSON.
const ordersSQL = `
SELECT coalesce(json_agg(o ORDER BY o.id DESC), '[]')::text FROM (
  SELECT o.id, u.username, o.total, o.status, o.created_at,
    (SELECT json_agg(json_build_object('name', m.name, 'qty', oi.qty, 'price', oi.price, 'options', oi.options) ORDER BY oi.id)
       FROM order_items oi JOIN menu_items m ON m.id = oi.menu_id
      WHERE oi.order_id = o.id) AS items
  FROM orders o JOIN users u ON u.id = o.user_id
  WHERE $1::int IS NULL OR o.user_id = $1
  ORDER BY o.status IN ('done', 'cancelled'), o.id DESC LIMIT 100
) o`

func main() {
	db, err := pgxpool.New(context.Background(), getDatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(context.Background()); err != nil {
		log.Fatal("เชื่อมต่อ Database ไม่สำเร็จ:", err)
	}

	mux := http.NewServeMux()

	// Public API
	mux.HandleFunc("GET /api/menu", func(w http.ResponseWriter, r *http.Request) {
		jsonQuery(w, r, db,
			`SELECT coalesce(json_agg(m ORDER BY m.id), '[]')::text
 			FROM menu_items m
 			WHERE m.is_available = TRUE`,
		)
	})

	mux.HandleFunc("POST /api/register", func(w http.ResponseWriter, r *http.Request) {
		register(w, r, db)
	})

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		login(w, r, db)
	})

	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		logout(w, r, db)
	})

	mux.Handle("GET /api/me", auth(db)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"username": getUsername(r),
				"role":     getRole(r),
			})
		}),
	))

	// Customer API
	mux.Handle("GET /api/my/orders", auth(db, "customer")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonQuery(w, r, db, ordersSQL, getUserID(r))
		}),
	))

	mux.Handle("POST /api/orders", auth(db, "customer")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			createOrder(w, r, db)
		}),
	))

	// Staff API
	mux.Handle("GET /api/staff/orders", auth(db, "staff")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonQuery(w, r, db, ordersSQL, nil)
		}),
	))

	mux.Handle("PATCH /api/staff/orders/{id}", auth(db, "staff")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setStatus(w, r, db)
		}),
	))

	mux.Handle("DELETE /api/admin/menu/{id}", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deleteMenu(w, r, db)
		}),
	))
	// Admin API
	mux.Handle("GET /api/admin/menu", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonQuery(w, r, db,
				`SELECT coalesce(json_agg(m ORDER BY m.id), '[]')::text
				 FROM menu_items m`,
			)
		}),
	))
	mux.Handle("POST /api/admin/menu", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			createMenu(w, r, db)
		}),
	))
	mux.Handle("PATCH /api/admin/menu/{id}", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			updateMenu(w, r, db)
		}),
	))

	// Admin Order API
	mux.Handle("GET /api/admin/orders", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonQuery(w, r, db, ordersSQL, nil)
		}),
	))

	mux.Handle("PATCH /api/admin/orders/{id}", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setStatus(w, r, db)
		}),
	))

	// Admin Dashboard API
	mux.Handle("GET /api/admin/summary", auth(db, "admin")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var summary struct {
				OrderCount  int `json:"order_count"`
				TotalSales  int `json:"total_sales"`
				MemberCount int `json:"member_count"`
			}

			err := db.QueryRow(r.Context(), `
              SELECT
                  (SELECT COUNT(*) FROM orders),
                  (SELECT COALESCE(SUM(total), 0) FROM orders WHERE status = 'done'),
                  (SELECT COUNT(*) FROM users WHERE role = 'customer')
          `).Scan(
				&summary.OrderCount,
				&summary.TotalSales,
				&summary.MemberCount,
			)

			if err != nil {
				serverError(w, err)
				return
			}

			writeJSON(w, http.StatusOK, summary)
		}),
	))

	port := strings.TrimSpace(os.Getenv("PORT"))
if port == "" {
    port = "8080"
}

log.Println("Server running on port " + port)

if err := http.ListenAndServe(":"+port, cors(mux)); err != nil {
    log.Fatal(err)
}
}

// getDatabaseURL reads the database connection string.
func getDatabaseURL() string {
	return strings.TrimSpace(os.Getenv("DATABASE_URL"))
}

// jsonQuery returns JSON generated by PostgreSQL.
func jsonQuery(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, sql string, args ...any) {
	var out string

	if err := db.QueryRow(r.Context(), sql, args...).Scan(&out); err != nil {
		serverError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

// createOrder creates a new order and its items in one transaction.
func createOrder(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var req orderReq

	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	if err := req.validate(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}

	defer tx.Rollback(context.Background())

	var id, total int

	err = tx.QueryRow(r.Context(),
		`INSERT INTO orders (user_id) VALUES ($1) RETURNING id`,
		getUserID(r),
	).Scan(&id)

	if err != nil {
		serverError(w, err)
		return
	}

	for _, it := range req.Items {
		var price int
		var groups []optionGroup

		err := tx.QueryRow(r.Context(),
			`SELECT price, options FROM menu_items WHERE id = $1 AND is_available = TRUE`,
			it.MenuID,
		).Scan(&price, &groups)

		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, http.StatusBadRequest, "ไม่พบเมนูที่เลือก")
			return
		}

		if err != nil {
			serverError(w, err)
			return
		}

		opts, err := pickOptions(groups, it.Options)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}

		_, err = tx.Exec(r.Context(), `
			INSERT INTO order_items (order_id, menu_id, qty, price, options)
			VALUES ($1, $2, $3, $4, $5)
		`, id, it.MenuID, it.Qty, price, opts)

		if err != nil {
			serverError(w, err)
			return
		}
	}

	err = tx.QueryRow(r.Context(), `
		UPDATE orders
		SET total = (
			SELECT sum(qty * price)
			FROM order_items
			WHERE order_id = $1
		)
		WHERE id = $1
		RETURNING total
	`, id).Scan(&total)

	if err != nil {
		serverError(w, err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]int{
		"id":    id,
		"total": total,
	})
}

// setStatus updates an order status.
func setStatus(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var body struct {
		Status string `json:"status"`
	}

	id, err := strconv.Atoi(r.PathValue("id"))

	if err != nil {
		fail(w, http.StatusBadRequest, "รหัสออเดอร์ไม่ถูกต้อง")
		return
	}

	if err := decodeJSON(r, &body); err != nil || !statuses[body.Status] {
		fail(w, http.StatusBadRequest, "สถานะไม่ถูกต้อง")
		return
	}

	tag, err := db.Exec(r.Context(),
		`UPDATE orders SET status = $1 WHERE id = $2`,
		body.Status, id,
	)

	if err != nil {
		serverError(w, err)
		return
	}

	if tag.RowsAffected() == 0 {
		fail(w, http.StatusNotFound, "ไม่พบออเดอร์")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// createMenu adds a new menu item.
func createMenu(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var req menuReq

	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Emoji = strings.TrimSpace(req.Emoji)
	req.Image = strings.TrimSpace(req.Image)

	if req.Name == "" || req.Emoji == "" {
		fail(w, http.StatusBadRequest, "กรุณาระบุชื่อเมนูและอีโมจิ")
		return
	}

	if req.Price < 0 {
		fail(w, http.StatusBadRequest, "ราคาต้องไม่ติดลบ")
		return
	}

	if req.Options == nil {
		req.Options = []optionGroup{}
	}

	optionsJSON, err := json.Marshal(req.Options)
	if err != nil {
		serverError(w, err)
		return
	}

	var id int

	err = db.QueryRow(r.Context(), `
		INSERT INTO menu_items
			(name, price, emoji, image, options, is_available)
		VALUES
			($1, $2, $3, NULLIF($4, ''), $5::jsonb, TRUE)
		RETURNING id
	`,
		req.Name,
		req.Price,
		req.Emoji,
		req.Image,
		string(optionsJSON),
	).Scan(&id)

	if err != nil {
		serverError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"name":         req.Name,
		"price":        req.Price,
		"is_available": true,
	})
}

// updateMenu updates an existing menu item.
func updateMenu(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	var req menuUpdateReq

	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "รหัสเมนูไม่ถูกต้อง")
		return
	}

	if err := decodeJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "ข้อมูลไม่ถูกต้อง")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Emoji = strings.TrimSpace(req.Emoji)
	req.Image = strings.TrimSpace(req.Image)

	if req.Name == "" || req.Emoji == "" {
		fail(w, http.StatusBadRequest, "กรุณาระบุชื่อเมนูและอีโมจิ")
		return
	}

	if req.Price < 0 {
		fail(w, http.StatusBadRequest, "ราคาต้องไม่ติดลบ")
		return
	}

	if req.Options == nil {
		req.Options = []optionGroup{}
	}

	optionsJSON, err := json.Marshal(req.Options)
	if err != nil {
		fail(w, http.StatusBadRequest, "ตัวเลือกเมนูไม่ถูกต้อง")
		return
	}

	tag, err := db.Exec(r.Context(),
		`UPDATE menu_items
		 SET name = $1,
		     price = $2,
		     emoji = $3,
		     image = NULLIF($4, ''),
		     options = $5::jsonb,
		     is_available = $6
		 WHERE id = $7`,
		req.Name,
		req.Price,
		req.Emoji,
		req.Image,
		optionsJSON,
		req.IsAvailable,
		id,
	)

	if err != nil {
		serverError(w, err)
		return
	}

	if tag.RowsAffected() == 0 {
		fail(w, http.StatusNotFound, "ไม่พบเมนู")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func deleteMenu(w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "รหัสเมนูไม่ถูกต้อง")
		return
	}

	tag, err := db.Exec(r.Context(),
		`DELETE FROM menu_items WHERE id = $1`,
		id,
	)
	if err != nil {
		// เมนูที่เคยถูกสั่งซื้อจะลบไม่ได้ เพราะมี order_items อ้างอิงอยู่
		if strings.Contains(err.Error(), "foreign key constraint") {
			fail(w, http.StatusConflict, "ไม่สามารถลบเมนูนี้ได้ เนื่องจากมีประวัติการสั่งซื้อ")
			return
		}
		serverError(w, err)
		return
	}

	if tag.RowsAffected() == 0 {
		fail(w, http.StatusNotFound, "ไม่พบเมนู")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON decodes a JSON request body.
func decodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	return decoder.Decode(dest)
}

// writeJSON sends a JSON response.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Println("เขียน JSON response ไม่สำเร็จ:", err)
	}
}

// fail sends an error response.
func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{
		"error": msg,
	})
}

// serverError logs the real error and sends a generic message.
func serverError(w http.ResponseWriter, err error) {
	log.Println(err)
	fail(w, http.StatusInternalServerError, "เกิดข้อผิดพลาดในระบบ")
}

// cors allows requests from the configured frontend.
func cors(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                origin := r.Header.Get("Origin")
                allowedOrigin := strings.TrimSpace(os.Getenv("FRONTEND_URL"))

                if origin != "" && origin == allowedOrigin {
                        w.Header().Set("Access-Control-Allow-Origin", origin)
                        w.Header().Set("Access-Control-Allow-Credentials", "true")
                        w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
                        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
                        w.Header().Set("Vary", "Origin")
                }

                if r.Method == "OPTIONS" {
                        w.WriteHeader(http.StatusNoContent)
                        return
                }

                next.ServeHTTP(w, r)
        })
}
