package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"mission1_cdn/pkg/storage"
)

// Config represents edge server runtime configuration.
type Config struct {
	NodeID         string
	Region         string
	Port           string
	PublicURL      string
	MongoURI       string
	DBName         string
	InternalSecret string
}

// Server is an edge node HTTP server interfacing with local MongoDB GridFS.
type Server struct {
	cfg   Config
	store *storage.GridFSStore
	mux   *http.ServeMux
}

// NewServer creates a new edge storage server.
func NewServer(cfg Config, store *storage.GridFSStore) *Server {
	s := &Server{
		cfg:   cfg,
		store: store,
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /files/{file_id}/{filename}", s.handleServeFile)
	s.mux.HandleFunc("POST /internal/replicate", s.handleReplicate)
	s.mux.HandleFunc("DELETE /internal/files/{file_id}", s.handleDelete)
}

// Handler returns the HTTP handler with global CORS & logging middleware.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Internal-Secret")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		start := time.Now()
		s.mux.ServeHTTP(w, r)
		log.Printf("[%s] %s %s (%s)", s.cfg.NodeID, r.Method, r.URL.Path, time.Since(start))
	})
}

// handleHealth reports node operational status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	mongoOK := true
	if err := s.store.Client.Ping(ctx, nil); err != nil {
		mongoOK = false
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"node_id":         s.cfg.NodeID,
		"region":          s.cfg.Region,
		"status":          "healthy",
		"mongo_connected": mongoOK,
		"public_url":      s.cfg.PublicURL,
		"timestamp":       time.Now().UTC(),
	})
}

// handleServeFile streams the requested file from MongoDB GridFS.
func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	filename := r.PathValue("filename")

	if fileID == "" {
		http.Error(w, "missing file_id", http.StatusBadRequest)
		return
	}

	// Cache validation via ETag
	etag := fmt.Sprintf(`"%s"`, fileID)
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	stream, err := s.store.OpenDownloadStream(ctx, fileID)
	if err != nil {
		http.Error(w, fmt.Sprintf("file %s not found on edge %s", fileID, s.cfg.NodeID), http.StatusNotFound)
		return
	}
	defer stream.Close()

	// Extract metadata
	fileInfo := stream.GetFile()
	contentType := "application/octet-stream"
	if len(fileInfo.Metadata) > 0 {
		var meta struct {
			ContentType string `bson:"content_type"`
		}
		if err := bson.Unmarshal(fileInfo.Metadata, &meta); err == nil && meta.ContentType != "" {
			contentType = meta.ContentType
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Length))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-CDN-Node", s.cfg.NodeID)
	w.Header().Set("X-CDN-Region", s.cfg.Region)
	if filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	}

	_, _ = io.Copy(w, stream)
}

// handleReplicate receives file replication payload from Central API (Server 2).
func (s *Server) handleReplicate(w http.ResponseWriter, r *http.Request) {
	// Verify internal secret
	secret := r.Header.Get("X-Internal-Secret")
	if secret == "" {
		auth := r.Header.Get("Authorization")
		secret = strings.TrimPrefix(auth, "Bearer ")
	}

	if s.cfg.InternalSecret != "" && secret != s.cfg.InternalSecret {
		http.Error(w, "unauthorized replication request", http.StatusUnauthorized)
		return
	}

	// 100MB max memory/temp file parser
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, fmt.Sprintf("failed to parse multipart form: %v", err), http.StatusBadRequest)
		return
	}

	fileID := r.FormValue("file_id")
	filename := r.FormValue("filename")
	contentType := r.FormValue("content_type")

	if fileID == "" || filename == "" {
		http.Error(w, "file_id and filename are required", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("file part missing: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// If file exists, delete it first to ensure clean overwrite
	exists, _ := s.store.Exists(ctx, fileID)
	if exists {
		_ = s.store.Delete(ctx, fileID)
	}

	written, err := s.store.Upload(ctx, fileID, filename, contentType, file)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to save to local gridfs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ready",
		"node_id":    s.cfg.NodeID,
		"region":     s.cfg.Region,
		"file_id":    fileID,
		"filename":   filename,
		"size_bytes": written,
		"url":        fmt.Sprintf("%s/files/%s/%s", s.cfg.PublicURL, fileID, filename),
		"synced_at":  time.Now().UTC(),
	})
}

// handleDelete removes file from local edge GridFS.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	secret := r.Header.Get("X-Internal-Secret")
	if secret == "" {
		auth := r.Header.Get("Authorization")
		secret = strings.TrimPrefix(auth, "Bearer ")
	}

	if s.cfg.InternalSecret != "" && secret != s.cfg.InternalSecret {
		http.Error(w, "unauthorized delete request", http.StatusUnauthorized)
		return
	}

	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "missing file_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := s.store.Delete(ctx, fileID); err != nil {
		http.Error(w, fmt.Sprintf("failed to delete file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "deleted",
		"node_id": s.cfg.NodeID,
		"file_id": fileID,
	})
}
