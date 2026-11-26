package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"mission1_cdn/pkg/models"
	"mission1_cdn/server1-webapp/geo"
)

type Config struct {
	Port          string
	CentralAPIURL string
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

type WebServer struct {
	cfg        Config
	httpClient *http.Client
	mux        *http.ServeMux
}

func main() {
	cfg := Config{
		Port:          getEnv("PORT", "3000"),
		CentralAPIURL: getEnv("CENTRAL_API_URL", "http://localhost:4000"),
	}

	log.Printf("Starting Webapp Frontend on port %s...", cfg.Port)
	log.Printf("Connecting to Central API at %s", cfg.CentralAPIURL)

	s := &WebServer{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		mux: http.NewServeMux(),
	}
	s.routes()

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      s.mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Printf("Webapp Portal running at http://localhost:%s", cfg.Port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Webapp server failure: %v", err)
	}
}

func (s *WebServer) routes() {
	// API routes
	s.mux.HandleFunc("GET /api/geo", s.handleGeoDetect)
	s.mux.HandleFunc("POST /api/upload", s.handleProxyUpload)
	s.mux.HandleFunc("GET /api/files", s.handleProxyListFiles)
	s.mux.HandleFunc("GET /api/files/{file_id}", s.handleProxyGetFile)
	s.mux.HandleFunc("DELETE /api/files/{file_id}", s.handleProxyDeleteFile)

	// CDN Geo-routing link
	s.mux.HandleFunc("GET /cdn/{file_id}/{filename}", s.handleCDNRoute)

	// Static UI
	fs := http.FileServer(http.Dir("./static"))
	s.mux.Handle("/", fs)
}

func (s *WebServer) handleGeoDetect(w http.ResponseWriter, r *http.Request) {
	info := geo.DetectClientRegion(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *WebServer) handleProxyUpload(w http.ResponseWriter, r *http.Request) {
	targetURL := fmt.Sprintf("%s/api/files/upload", s.cfg.CentralAPIURL)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to contact central api: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *WebServer) handleProxyListFiles(w http.ResponseWriter, r *http.Request) {
	targetURL := fmt.Sprintf("%s/api/files", s.cfg.CentralAPIURL)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to contact central api: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *WebServer) handleProxyGetFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	targetURL := fmt.Sprintf("%s/api/files/%s", s.cfg.CentralAPIURL, fileID)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to contact central api: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *WebServer) handleProxyDeleteFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	targetURL := fmt.Sprintf("%s/api/files/%s", s.cfg.CentralAPIURL, fileID)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, targetURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to contact central api: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// handleCDNRoute is the primary CDN routing endpoint:
// It detects the user's geographic location and automatically 302 redirects
// them to the closest replica server (Singapore, New York, London).
func (s *WebServer) handleCDNRoute(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	filename := r.PathValue("filename")

	geoInfo := geo.DetectClientRegion(r)

	// Fetch file metadata from Central API
	targetURL := fmt.Sprintf("%s/api/files/%s", s.cfg.CentralAPIURL, fileID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		http.Error(w, "internal routing error", http.StatusInternalServerError)
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "file not found in CDN registry", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	var fileDoc models.FileDocument
	if err := json.NewDecoder(resp.Body).Decode(&fileDoc); err != nil {
		http.Error(w, "failed to decode file record", http.StatusInternalServerError)
		return
	}

	// Find the replica matching the user's region
	var targetURLToRedirect string
	var chosenNode string

	for _, rep := range fileDoc.Replicas {
		if strings.EqualFold(rep.Region, geoInfo.Region) && rep.Status == models.StatusReady {
			targetURLToRedirect = rep.URL
			chosenNode = rep.NodeID
			break
		}
	}

	// If the regional replica is still syncing or unavailable, fallback to any ready replica
	if targetURLToRedirect == "" {
		for _, rep := range fileDoc.Replicas {
			if rep.Status == models.StatusReady {
				targetURLToRedirect = rep.URL
				chosenNode = rep.NodeID
				break
			}
		}
	}

	// Final fallback: use url_forward from MongoDB document
	if targetURLToRedirect == "" {
		targetURLToRedirect = fileDoc.URLForward
		chosenNode = "primary-forward"
	}

	log.Printf("[CDN Route] Client from IP %s (%s) requested %s/%s -> Routing to %s (%s)",
		geoInfo.ClientIP, geoInfo.Region, fileID, filename, chosenNode, targetURLToRedirect)

	w.Header().Set("X-CDN-Routed-Node", chosenNode)
	w.Header().Set("X-CDN-Client-Region", geoInfo.Region)
	http.Redirect(w, r, targetURLToRedirect, http.StatusFound)
}
