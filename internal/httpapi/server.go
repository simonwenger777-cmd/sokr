package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"sokr/internal/store"
)

const maxPayload = 100_000

var (
	codeRe = regexp.MustCompile(`^[a-z0-9]{4,32}$`)
	alpha  = []byte("abcdefghjkmnpqrstuvwxyz23456789")
)

var reserved = map[string]struct{}{
	"api": {}, "healthz": {}, "robots.txt": {}, "favicon.ico": {},
}

type Server struct {
	store store.Store
	token string
	page  []byte
	lim   *limiter
}

func New(st store.Store, token string, page []byte) *Server {
	return &Server{
		store: st,
		token: token,
		page:  page,
		lim:   newLimiter(30, time.Minute),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/create", s.create)
	mux.HandleFunc("GET /api/get/{code}", s.get)
	mux.HandleFunc("GET /fold.js", s.foldJS)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /", s.pageHandler)
	mux.HandleFunc("GET /{code}", s.pageHandler)
	return s.log(security(mux))
}

func (s *Server) foldJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(FoldJS)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}

func (s *Server) pageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		code := strings.ToLower(strings.Trim(r.PathValue("code"), "/"))
		if code == "" || !codeRe.MatchString(code) {
			http.NotFound(w, r)
			return
		}
		if _, skip := reserved[code]; skip {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.page)
}

type createReq struct {
	Payload string `json:"payload"`
	TTLSec  int    `json:"ttlSec"`
	Once    bool   `json:"once"`
	Alias   string `json:"alias"`
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if !authorized(r, s.token) {
		writeJSON(w, http.StatusUnauthorized, errBody("нужен токен создателя"))
		return
	}
	if !s.lim.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, errBody("слишком часто, подождите минуту"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPayload+4096)
	var req createReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errBody("строка длиннее 100 КБ"))
			return
		}
		writeJSON(w, http.StatusBadRequest, errBody("не понял запрос"))
		return
	}
	if req.Payload == "" || len(req.Payload) > maxPayload {
		writeJSON(w, http.StatusBadRequest, errBody("пустая строка или длиннее 100 КБ"))
		return
	}
	if req.TTLSec != 0 && (req.TTLSec < 60 || req.TTLSec > 365*24*3600) {
		writeJSON(w, http.StatusBadRequest, errBody("срок от минуты до года"))
		return
	}
	var exp *time.Time
	if req.TTLSec > 0 {
		t := time.Now().Add(time.Duration(req.TTLSec) * time.Second)
		exp = &t
	}
	rec := store.Record{Payload: req.Payload, Once: req.Once, ExpiresAt: exp}

	alias := strings.ToLower(strings.TrimSpace(req.Alias))
	if alias != "" {
		if !codeRe.MatchString(alias) {
			writeJSON(w, http.StatusBadRequest, errBody("ключ: 4–32 символа, латиница и цифры"))
			return
		}
		if _, skip := reserved[alias]; skip {
			writeJSON(w, http.StatusBadRequest, errBody("этот ключ занят системой"))
			return
		}
		if err := s.store.Put(r.Context(), alias, rec); err != nil {
			writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"code": alias})
		return
	}

	var last error
	for i := 0; i < 6; i++ {
		code, err := randomCode(8)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errBody("не удалось собрать ключ"))
			return
		}
		last = s.store.Put(r.Context(), code, rec)
		if last == nil {
			writeJSON(w, http.StatusCreated, map[string]string{"code": code})
			return
		}
		if !errors.Is(last, store.ErrExists) {
			writeStoreErr(w, last)
			return
		}
	}
	writeStoreErr(w, last)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	code := strings.ToLower(strings.TrimSpace(r.PathValue("code")))
	if !codeRe.MatchString(code) {
		writeJSON(w, http.StatusNotFound, errBody("нет такой строки"))
		return
	}
	payload, err := s.store.Take(r.Context(), code)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"payload": payload})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errBody("нет такой строки или она уже сгорела"))
		return
	}
	if errors.Is(err, store.ErrExists) {
		writeJSON(w, http.StatusConflict, errBody("такой ключ уже есть"))
		return
	}
	log.Printf("store error: %v", err)
	writeJSON(w, http.StatusServiceUnavailable, errBody("хранилище недоступно"))
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(got, prefix)), []byte(token)) == 1
}

func randomCode(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range buf {
		out[i] = alpha[int(buf[i])%len(alpha)]
	}
	return string(out), nil
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, routeLabel(r.URL.Path), rw.status, time.Since(start).Round(time.Millisecond))
	})
}

func routeLabel(path string) string {
	if strings.HasPrefix(path, "/api/get/") {
		return "/api/get/:code"
	}
	if path != "/" && !strings.HasPrefix(path, "/api/") && path != "/healthz" && path != "/robots.txt" {
		return "/:code"
	}
	return path
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 20000 {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-l.window)
	prev := l.hits[key]
	keep := make([]time.Time, 0, len(prev)+1)
	for _, t := range prev {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}
