package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
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
	return &Server{client: c, addr: DefaultBindAddress, uiPath: ""}
}

// newLoopbackRequest builds a request addressed to the default loopback bind, as
// a browser on the far side of kubectl port-forward would send it.
func newLoopbackRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Host = DefaultBindAddress
	return req
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
	if resp.Version != "0.2.0" {
		t.Errorf("version: got %q, want %q", resp.Version, "0.2.0")
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

// TestHandler_NoCORSHeaders guards against re-adding "Access-Control-Allow-Origin: *":
// with it, any web page open in the operator's browser could read the API.
func TestHandler_NoCORSHeaders(t *testing.T) {
	h := newTestServer(t).Handler()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "health", method: http.MethodGet, path: "/api/health", wantStatus: http.StatusOK},
		{name: "list stackupgrades", method: http.MethodGet, path: "/api/stackupgrades", wantStatus: http.StatusOK},
		{name: "list policies", method: http.MethodGet, path: "/api/compatibilitypolicies", wantStatus: http.StatusOK},
		{name: "cross-origin approve", method: http.MethodPost, path: "/api/stackupgrades/default/missing/approve", wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := newLoopbackRequest(tc.method, tc.path)
			req.Header.Set("Origin", "https://evil.example")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin: got %q, want no header", got)
			}
		})
	}
}

// TestHandler_PreflightNotAnswered checks that a cross-origin preflight for the
// mutating approve endpoint is not granted, so browsers never send the POST.
func TestHandler_PreflightNotAnswered(t *testing.T) {
	h := newTestServer(t).Handler()
	req := newLoopbackRequest(http.MethodOptions, "/api/stackupgrades/default/demo/approve")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods"} {
		if got := rec.Header().Get(header); got != "" {
			t.Errorf("%s: got %q, want no header", header, got)
		}
	}
}

// TestHandler_CrossOriginWriteRejected covers CSRF on the approve endpoint: a bodyless
// POST is a "simple" request, so a hostile page could send it without any preflight.
// Same-origin writes (the UI itself, or the Vite dev proxy) must still get through.
func TestHandler_CrossOriginWriteRejected(t *testing.T) {
	h := newTestServer(t).Handler()
	const path = "/api/stackupgrades/default/missing/approve"

	tests := []struct {
		name       string
		headers    map[string]string
		wantStatus int
	}{
		{
			name:       "cross-site fetch",
			headers:    map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "older browser, foreign Origin",
			headers:    map[string]string{"Origin": "https://evil.example"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "same-origin UI",
			headers:    map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://127.0.0.1:8082"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "non-browser client",
			headers:    nil,
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := newLoopbackRequest(http.MethodPost, path)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

// TestHandler_HostCheck covers DNS rebinding: once a hostile domain re-resolves to
// 127.0.0.1 the browser treats it as same-origin, so only the Host header gives it away.
func TestHandler_HostCheck(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	tests := []struct {
		name       string
		bindAddr   string
		host       string
		wantStatus int
	}{
		{name: "loopback bind, 127.0.0.1", bindAddr: DefaultBindAddress, host: "127.0.0.1:8082", wantStatus: http.StatusOK},
		{name: "loopback bind, localhost", bindAddr: DefaultBindAddress, host: "localhost:8082", wantStatus: http.StatusOK},
		{name: "loopback bind, vite dev proxy", bindAddr: DefaultBindAddress, host: "localhost:5173", wantStatus: http.StatusOK},
		{name: "loopback bind, IPv6 loopback", bindAddr: DefaultBindAddress, host: "[::1]:8082", wantStatus: http.StatusOK},
		{name: "loopback bind, rebound domain", bindAddr: DefaultBindAddress, host: "evil.example:8082", wantStatus: http.StatusForbidden},
		{name: "loopback bind, no port", bindAddr: DefaultBindAddress, host: "evil.example", wantStatus: http.StatusForbidden},
		// Binding beyond loopback is an explicit choice (e.g. behind an Ingress),
		// so the Host check does not apply there.
		{name: "all-interfaces bind, ingress host", bindAddr: ":8082", host: "viltrumite.example.com", wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := (&Server{client: c, addr: tc.bindAddr}).Handler()
			req := httptest.NewRequest(http.MethodGet, "/api/stackupgrades", nil)
			req.Host = tc.host
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: DefaultBindAddress, want: true},
		{addr: "127.0.0.1:0", want: true},
		{addr: "localhost:8082", want: true},
		{addr: "[::1]:8082", want: true},
		{addr: ":8082", want: false},
		{addr: "0.0.0.0:8082", want: false},
		{addr: "10.0.0.5:8082", want: false},
		{addr: "8082", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if got := isLoopback(tc.addr); got != tc.want {
				t.Errorf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}
