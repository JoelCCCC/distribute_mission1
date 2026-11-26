package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"mission1_cdn/pkg/models"
	"mission1_cdn/pkg/storage"
	"mission1_cdn/server2-central-api/replicator"
)

type Config struct {
	Port           string
	MongoURI       string
	DBName         string
	InternalSecret string
	Nodes          []models.NodeConfig
	PrimaryNodeID  string
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func parseNodesConfig() []models.NodeConfig {
	// Check if JSON configuration is provided
	if raw := os.Getenv("EDGE_NODES_JSON"); raw != "" {
		var nodes []models.NodeConfig
		if err := json.Unmarshal([]byte(raw), &nodes); err == nil && len(nodes) > 0 {
			return nodes
		}
	}

	// Default fallback configurations
	sgHost := getEnv("EDGE_SG_HOST", "localhost:5001")
	sgURL := getEnv("EDGE_SG_URL", fmt.Sprintf("http://%s", sgHost))
	sgInternalURL := getEnv("EDGE_SG_INTERNAL_URL", sgURL)

	nyHost := getEnv("EDGE_NY_HOST", "localhost:5002")
	nyURL := getEnv("EDGE_NY_URL", fmt.Sprintf("http://%s", nyHost))
	nyInternalURL := getEnv("EDGE_NY_INTERNAL_URL", nyURL)

	ldnHost := getEnv("EDGE_LDN_HOST", "localhost:5003")
	ldnURL := getEnv("EDGE_LDN_URL", fmt.Sprintf("http://%s", ldnHost))
	ldnInternalURL := getEnv("EDGE_LDN_INTERNAL_URL", ldnURL)

	return []models.NodeConfig{
		{
			NodeID:      "sg-1",
			Region:      "Asia",
			Host:        sgHost,
			PublicURL:   sgURL,
			InternalURL: sgInternalURL,
		},
		{
			NodeID:      "ny-1",
			Region:      "North America",
			Host:        nyHost,
			PublicURL:   nyURL,
			InternalURL: nyInternalURL,
		},
		{
			NodeID:      "ldn-1",
			Region:      "Europe",
			Host:        ldnHost,
			PublicURL:   ldnURL,
			InternalURL: ldnInternalURL,
		},
	}
}

type CentralServer struct {
	cfg        Config
	store      *storage.GridFSStore
	filesColl  *mongo.Collection
	replicator *replicator.Replicator
	mux        *http.ServeMux
}

func generateFileID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("img_%d_%s", time.Now().Unix()%1000000, hex.EncodeToString(b))
}

func main() {
	cfg := Config{
		Port:           getEnv("PORT", "4000"),
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:         getEnv("DB_NAME", "cdn_central"),
		InternalSecret: getEnv("INTERNAL_SECRET", "cdn-super-secret-key-2026"),
		PrimaryNodeID:  getEnv("PRIMARY_NODE_ID", "sg-1"),
		Nodes:          parseNodesConfig(),
	}

	log.Printf("Starting Central API Server on port %s...", cfg.Port)
	log.Printf("Configured %d Edge Storage Nodes:", len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		log.Printf("  - [%s] %s -> %s (%s)", n.NodeID, n.Region, n.PublicURL, n.Host)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := storage.NewGridFSStore(ctx, cfg.MongoURI, cfg.DBName)
	if err != nil {
		log.Fatalf("Failed to connect to Central MongoDB at %s: %v", cfg.MongoURI, err)
	}
	defer store.Close(context.Background())

	filesColl := store.DB.Collection("files")
	// Ensure index on file_id
	_, _ = filesColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "file_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	rep := replicator.NewReplicator(store, filesColl, cfg.InternalSecret)

	server := &CentralServer{
		cfg:        cfg,
		store:      store,
		filesColl:  filesColl,
		replicator: rep,
		mux:        http.NewServeMux(),
	}
	server.routes()

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      server.corsMiddleware(server.mux),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	log.Printf("Central API Server is running at http://localhost:%s", cfg.Port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Central API server failure: %v", err)
	}
}

func (s *CentralServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Client-Region")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[CentralAPI] %s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *CentralServer) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/files/upload", s.handleUpload)
	s.mux.HandleFunc("GET /api/files", s.handleListFiles)
	s.mux.HandleFunc("GET /api/files/{file_id}", s.handleGetFile)
	s.mux.HandleFunc("GET /api/files/{file_id}/route", s.handleRouteFile)
	s.mux.HandleFunc("DELETE /api/files/{file_id}", s.handleDeleteFile)
}

func (s *CentralServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	mongoOK := true
	if err := s.store.Client.Ping(ctx, nil); err != nil {
		mongoOK = false
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"service":         "central-api",
		"status":          "healthy",
		"mongo_connected": mongoOK,
		"edge_nodes":      len(s.cfg.Nodes),
		"timestamp":       time.Now().UTC(),
	})
}

func (s *CentralServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	// Parse 150MB multipart form
	if err := r.ParseMultipartForm(150 << 20); err != nil {
		http.Error(w, fmt.Sprintf("failed to parse multipart: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' in upload form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := header.Filename
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Read into memory or write through hasher to GridFS
	hasher := sha256.New()
	b, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read file: %v", err), http.StatusInternalServerError)
		return
	}
	hasher.Write(b)
	shaHex := hex.EncodeToString(hasher.Sum(nil))
	sizeBytes := int64(len(b))

	// Allow custom file_id or generate one
	fileID := r.FormValue("file_id")
	if fileID == "" {
		fileID = generateFileID()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// 1. Upload to Central MongoDB GridFS
	_, err = s.store.Upload(ctx, fileID, filename, contentType, strings.NewReader(string(b)))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to store in central gridfs: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Build replicas array
	now := time.Now().UTC()
	var replicas []models.ReplicaNode
	var primaryURLForward string

	for _, node := range s.cfg.Nodes {
		url := fmt.Sprintf("%s/files/%s/%s", node.PublicURL, fileID, filename)
		repNode := models.ReplicaNode{
			NodeID: node.NodeID,
			Region: node.Region,
			Host:   node.Host,
			URL:    url,
			Status: models.StatusSyncing,
		}
		replicas = append(replicas, repNode)

		if node.NodeID == s.cfg.PrimaryNodeID || primaryURLForward == "" {
			primaryURLForward = url
		}
	}

	fileDoc := models.FileDocument{
		FileID:      fileID,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		SHA256:      shaHex,
		URLForward:  primaryURLForward,
		CreatedAt:   now,
		UpdatedAt:   now,
		Replicas:    replicas,
	}

	// 3. Store metadata in Central MongoDB 'files' collection
	_, err = s.filesColl.InsertOne(ctx, fileDoc)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to record file metadata in mongodb: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Asynchronously dispatch background replication to all edge nodes
	s.replicator.DispatchAsync(&fileDoc, s.cfg.Nodes)

	// 5. Return immediate success response to user
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(fileDoc)
}

func (s *CentralServer) handleListFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	findOpts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100)
	cursor, err := s.filesColl.Find(ctx, bson.M{}, findOpts)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query files: %v", err), http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var files []models.FileDocument
	if err := cursor.All(ctx, &files); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode files: %v", err), http.StatusInternalServerError)
		return
	}

	if files == nil {
		files = []models.FileDocument{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (s *CentralServer) handleGetFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "missing file_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var fileDoc models.FileDocument
	err := s.filesColl.FindOne(ctx, bson.M{"file_id": fileID}).Decode(&fileDoc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fileDoc)
}

func (s *CentralServer) handleRouteFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	targetRegion := r.URL.Query().Get("region")
	if targetRegion == "" {
		targetRegion = r.Header.Get("X-Client-Region")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var fileDoc models.FileDocument
	err := s.filesColl.FindOne(ctx, bson.M{"file_id": fileID}).Decode(&fileDoc)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Match replica in requested region
	var bestReplica *models.ReplicaNode
	for i, rep := range fileDoc.Replicas {
		if strings.EqualFold(rep.Region, targetRegion) {
			bestReplica = &fileDoc.Replicas[i]
			break
		}
	}

	// Fallback to ready replica or primary url_forward
	if bestReplica == nil && len(fileDoc.Replicas) > 0 {
		for i, rep := range fileDoc.Replicas {
			if rep.Status == models.StatusReady {
				bestReplica = &fileDoc.Replicas[i]
				break
			}
		}
		if bestReplica == nil {
			bestReplica = &fileDoc.Replicas[0]
		}
	}

	if r.URL.Query().Get("redirect") == "true" && bestReplica != nil {
		http.Redirect(w, r, bestReplica.URL, http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"file_id":        fileDoc.FileID,
		"filename":       fileDoc.Filename,
		"target_region":  targetRegion,
		"chosen_replica": bestReplica,
		"url_forward":    fileDoc.URLForward,
	})
}

func (s *CentralServer) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	if fileID == "" {
		http.Error(w, "missing file_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// 1. Delete from Central MongoDB collection
	_, err := s.filesColl.DeleteOne(ctx, bson.M{"file_id": fileID})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to delete from db: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Delete from Central GridFS
	_ = s.store.Delete(ctx, fileID)

	// 3. Cascade delete across all edge nodes
	s.replicator.CascadeDelete(fileID, s.cfg.Nodes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "deleted",
		"file_id": fileID,
	})
}
