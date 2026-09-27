package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// DefaultBindAddress keeps the UI on the pod's loopback interface. The server
// has no authentication yet and its approve endpoint mutates the cluster, so it
// must only be reachable through kubectl port-forward until auth lands (S37).
const DefaultBindAddress = "127.0.0.1:8082"

// Server serves the React UI and JSON API endpoints.
type Server struct {
	client client.Client
	addr   string
	uiPath string
}

// NewServer creates a Server backed by the manager's client that listens on
// addr (host:port, see DefaultBindAddress).
func NewServer(mgr manager.Manager, addr, uiPath string) *Server {
	return &Server{
		client: mgr.GetClient(),
		addr:   addr,
		uiPath: uiPath,
	}
}

// Handler returns the routed HTTP handler. The UI and API share one origin, so
// no CORS headers are sent and browsers block cross-origin reads. Two more
// browser guards sit in front of the routes:
//   - CrossOriginProtection rejects cross-origin writes (403). The approve POST
//     has no body, which makes it a "simple" request that browsers send without
//     a preflight, so dropping CORS headers alone does not stop it.
//   - On a loopback bind, loopbackHostOnly rejects requests whose Host is not a
//     loopback name, which blocks DNS rebinding.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/stackupgrades", s.handleListStackUpgrades)
	mux.HandleFunc("GET /api/stackupgrades/{namespace}/{name}", s.handleGetStackUpgrade)
	mux.HandleFunc("POST /api/stackupgrades/{namespace}/{name}/approve", s.handleApproveStackUpgrade)
	mux.HandleFunc("GET /api/compatibilitypolicies", s.handleListCompatibilityPolicies)

	if s.uiPath != "" {
		mux.Handle("/", http.FileServer(http.Dir(s.uiPath)))
	}

	var h http.Handler = http.NewCrossOriginProtection().Handler(mux)
	if isLoopback(s.addr) {
		h = loopbackHostOnly(h)
	}
	return loggingMiddleware(h)
}

// Start begins serving HTTP. Blocks until ctx is cancelled or a fatal error occurs.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if !isLoopback(s.addr) {
		slog.Warn("ui server is reachable beyond loopback and has no authentication; anyone who can connect can approve upgrades",
			"addr", s.addr)
	}
	slog.Info("ui server starting", "addr", s.addr, "uiPath", s.uiPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("ui server: %w", err)
	}
	return nil
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: "0.1.0"})
}

type stackUpgradeView struct {
	Name      string                                    `json:"name"`
	Namespace string                                    `json:"namespace"`
	Spec      kubeviltrumitev1alpha1.StackUpgradeSpec   `json:"spec"`
	Status    kubeviltrumitev1alpha1.StackUpgradeStatus `json:"status"`
}

func (s *Server) handleListStackUpgrades(w http.ResponseWriter, r *http.Request) {
	list := &kubeviltrumitev1alpha1.StackUpgradeList{}
	if err := s.client.List(r.Context(), list); err != nil {
		http.Error(w, fmt.Sprintf("list failed: %v", err), http.StatusInternalServerError)
		return
	}
	views := make([]stackUpgradeView, 0, len(list.Items))
	for _, item := range list.Items {
		views = append(views, stackUpgradeView{
			Name:      item.Name,
			Namespace: item.Namespace,
			Spec:      item.Spec,
			Status:    item.Status,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGetStackUpgrade(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	su := &kubeviltrumitev1alpha1.StackUpgrade{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, su); err != nil {
		http.Error(w, fmt.Sprintf("not found: %v", err), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, stackUpgradeView{
		Name:      su.Name,
		Namespace: su.Namespace,
		Spec:      su.Spec,
		Status:    su.Status,
	})
}

func (s *Server) handleApproveStackUpgrade(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	su := &kubeviltrumitev1alpha1.StackUpgrade{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, su); err != nil {
		http.Error(w, fmt.Sprintf("not found: %v", err), http.StatusNotFound)
		return
	}

	patch := client.MergeFrom(su.DeepCopy())
	su.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseApproved
	if err := s.client.Status().Patch(r.Context(), su, patch); err != nil {
		http.Error(w, fmt.Sprintf("patch failed: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, stackUpgradeView{
		Name:      su.Name,
		Namespace: su.Namespace,
		Spec:      su.Spec,
		Status:    su.Status,
	})
}

type compatibilityPolicyView struct {
	Name      string                                         `json:"name"`
	Namespace string                                         `json:"namespace"`
	Spec      kubeviltrumitev1alpha1.CompatibilityPolicySpec `json:"spec"`
}

func (s *Server) handleListCompatibilityPolicies(w http.ResponseWriter, r *http.Request) {
	list := &kubeviltrumitev1alpha1.CompatibilityPolicyList{}
	if err := s.client.List(r.Context(), list); err != nil {
		http.Error(w, fmt.Sprintf("list failed: %v", err), http.StatusInternalServerError)
		return
	}
	views := make([]compatibilityPolicyView, 0, len(list.Items))
	for _, item := range list.Items {
		views = append(views, compatibilityPolicyView{
			Name:      item.Name,
			Namespace: item.Namespace,
			Spec:      item.Spec,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// isLoopback reports whether addr (host:port) binds only to a loopback
// interface. An empty host (":8082") binds every interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return isLoopbackHost(host)
}

// isLoopbackHost reports whether host (no port) is "localhost" or a loopback IP.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// loopbackHostOnly rejects requests whose Host header is not a loopback name.
// It blocks DNS rebinding: a hostile page whose domain re-resolves to 127.0.0.1
// is same-origin to the browser, so CrossOriginProtection lets it through, but
// the browser still sends that domain as Host.
func loopbackHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !isLoopbackHost(host) {
			http.Error(w, "forbidden: the UI only answers requests addressed to localhost", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("http", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
