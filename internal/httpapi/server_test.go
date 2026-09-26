package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sokr/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Memory) {
	t.Helper()
	mem := store.NewMemory()
	h := New(mem, "test-token", Page).Handler()
	return httptest.NewServer(h), mem
}

func TestCreateAndRead(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	res := postJSON(t, srv.URL+"/api/create", "test-token", map[string]any{
		"payload": "логин: neo\nпароль: matrix",
		"ttlSec":  3600,
		"once":    false,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d body %s", res.StatusCode, res.body)
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(res.body, &created); err != nil || len(created.Code) != 8 {
		t.Fatalf("code %#v err %v", created, err)
	}

	got := getJSON(t, srv.URL+"/api/get/"+created.Code)
	if got.StatusCode != http.StatusOK || !strings.Contains(string(got.body), "matrix") {
		t.Fatalf("get %d %s", got.StatusCode, got.body)
	}
	page := getJSON(t, srv.URL+"/"+created.Code)
	if page.StatusCode != http.StatusOK || !bytes.Contains(page.body, []byte("Сокращатель")) {
		t.Fatalf("page %d", page.StatusCode)
	}
	if bytes.Contains(page.body, []byte("matrix")) {
		t.Fatal("html must not contain the payload")
	}
}

func TestOnceBurns(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	res := postJSON(t, srv.URL+"/api/create", "test-token", map[string]any{
		"payload": "one-shot",
		"ttlSec":  0,
		"once":    true,
	})
	var created struct{ Code string }
	_ = json.Unmarshal(res.body, &created)
	first := getJSON(t, srv.URL+"/api/get/"+created.Code)
	second := getJSON(t, srv.URL+"/api/get/"+created.Code)
	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusNotFound {
		t.Fatalf("first %d second %d", first.StatusCode, second.StatusCode)
	}
}

func TestAuthAndValidation(t *testing.T) {
	srv, mem := newTestServer(t)
	defer srv.Close()
	if postJSON(t, srv.URL+"/api/create", "", map[string]any{"payload": "x", "ttlSec": 0}).StatusCode != http.StatusUnauthorized {
		t.Fatal("missing token")
	}
	if postJSON(t, srv.URL+"/api/create", "nope", map[string]any{"payload": "x", "ttlSec": 0}).StatusCode != http.StatusUnauthorized {
		t.Fatal("bad token")
	}
	if postJSON(t, srv.URL+"/api/create", "test-token", map[string]any{"payload": "", "ttlSec": 0}).StatusCode != http.StatusBadRequest {
		t.Fatal("empty")
	}
	if postJSON(t, srv.URL+"/api/create", "test-token", map[string]any{"payload": "x", "ttlSec": 0, "alias": "API"}).StatusCode != http.StatusBadRequest {
		t.Fatal("reserved alias should be rejected after lowercasing")
	}
	ok := postJSON(t, srv.URL+"/api/create", "test-token", map[string]any{"payload": "custom", "ttlSec": 60, "alias": "Shop4"})
	if ok.StatusCode != http.StatusCreated || !bytes.Contains(ok.body, []byte("shop4")) {
		t.Fatalf("alias %d %s", ok.StatusCode, ok.body)
	}
	past := time.Now().Add(-time.Minute)
	if err := mem.Put(context.Background(), "gone", store.Record{Payload: "old", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if getJSON(t, srv.URL+"/api/get/gone").StatusCode != http.StatusNotFound {
		t.Fatal("expired key must 404")
	}
}

func TestHealthAndHome(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()
	home := getJSON(t, srv.URL+"/")
	if home.StatusCode != http.StatusOK || !bytes.Contains(home.body, []byte("Короткий ключ")) {
		t.Fatalf("home %d", home.StatusCode)
	}
	health := getJSON(t, srv.URL+"/healthz")
	if health.StatusCode != http.StatusOK || !bytes.Contains(health.body, []byte(`"ok":true`)) {
		t.Fatalf("health %s", health.body)
	}
}

type response struct {
	StatusCode int
	body       []byte
}

func postJSON(t *testing.T, url, token string, v any) response {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return response{StatusCode: res.StatusCode, body: body}
}

func getJSON(t *testing.T, url string) response {
	t.Helper()
	res, err := http.DefaultClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return response{StatusCode: res.StatusCode, body: body}
}
