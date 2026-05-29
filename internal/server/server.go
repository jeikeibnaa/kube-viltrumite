package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Server serves the React UI and JSON API endpoints.
type Server struct {
	client client.Client
	port   int
	uiPath string
}

// NewServer creates a Server backed by the manager's client.
func NewServer(mgr manager.Manager, port int, uiPath string) *Server {
	return &Server{
		client: mgr.GetClient(),
		port:   port,
		uiPath: uiPath,
	}
}

// Start begins serving HTTP. Blocks until ctx is cancelled or a fatal error occurs.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/stackupgrades", s.handleListStackUpgrades)
	mux.HandleFunc("GET /api/stackupgrades/{namespace}/{name}", s.handleGetStackUpgrade)
	mux.HandleFunc("POST /api/stackupgrades/{namespace}/{name}/approve", s.handleApproveStackUpgrade)
	mux.HandleFunc("GET /api/compatibilitypolicies", s.handleListCompatibilityPolicies)

	if s.uiPath != "" {
		mux.Handle("/", http.FileServer(http.Dir(s.uiPath)))
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: corsMiddleware(loggingMiddleware(mux)),
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	slog.Info("ui server starting", "port", s.port, "uiPath", s.uiPath)
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
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
