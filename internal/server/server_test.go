package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme clientgo: %v", err)
	}
	if err := kubeviltrumitev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	return s
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	return &Server{client: c, port: 8082, uiPath: ""}
}

func TestHealth(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want Content-Type application/json, got %q", ct)
	}
	var resp healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status: got %q, want %q", resp.Status, "ok")
	}
	if resp.Version != "0.1.0" {
		t.Errorf("version: got %q, want %q", resp.Version, "0.1.0")
	}
}

func TestListStackUpgrades_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/stackupgrades", nil)
	rec := httptest.NewRecorder()

	s.handleListStackUpgrades(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d", rec.Code)
	}
	var items []stackUpgradeView
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("want empty array, got %d items", len(items))
	}
}
