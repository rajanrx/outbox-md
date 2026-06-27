package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSafeJoinRejectsTraversal(t *testing.T) {
	if _, err := safeJoin("/data", "../etc/passwd"); err == nil {
		t.Fatal("expected traversal rejection for ../etc/passwd")
	}
	if _, err := safeJoin("/data", "nested/../../escape"); err == nil {
		t.Fatal("expected traversal rejection for nested escape")
	}
	if got, err := safeJoin("/data", "spec.md"); err != nil || got != "/data/spec.md" {
		t.Fatalf("safeJoin(spec.md) = %q, %v", got, err)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want \"ok\"", rec.Body.String())
	}
}
