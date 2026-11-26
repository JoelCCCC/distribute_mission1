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
	"sync"
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
	mu         sync.RWMutex
	store      *storage.GridFSStore
	filesColl  *mongo.Collection
	nodesColl  *mongo.Collection
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

	nodesColl := store.DB.Collection("nodes")
	_, _ = nodesColl.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "node_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})

	// Load existing nodes from MongoDB or seed defaults
	cursor, err := nodesColl.Find(ctx, bson.M{})
	if err == nil {
		var dbNodes []models.NodeConfig
		if err := cursor.All(ctx, &dbNodes); err == nil && len(dbNodes) > 0 {
			cfg.Nodes = dbNodes
			log.Printf("Loaded %d active edge nodes from MongoDB registry", len(dbNodes))
		} else {
			for i := range cfg.Nodes {
				cfg.Nodes[i].Status = "active"
				cfg.Nodes[i].CreatedAt = time.Now().UTC()
				_, _ = nodesColl.InsertOne(ctx, cfg.Nodes[i])
			}
			log.Printf("Seeded %d default edge nodes into MongoDB registry", len(cfg.Nodes))
		}
	}

	rep := replicator.NewReplicator(store, filesColl, cfg.InternalSecret)

	server := &CentralServer{
		cfg:        cfg,
		store:      store,
		filesColl:  filesColl,
		nodesColl:  nodesColl,
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

func (s *CentralServer) getNodes() []models.NodeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodesCopy := make([]models.NodeConfig, len(s.cfg.Nodes))
	copy(nodesCopy, s.cfg.Nodes)
	return nodesCopy
}

func (s *CentralServer) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/files/upload", s.handleUpload)
	s.mux.HandleFunc("GET /api/files", s.handleListFiles)
	s.mux.HandleFunc("GET /api/files/{file_id}", s.handleGetFile)
	s.mux.HandleFunc("GET /api/files/{file_id}/route", s.handleRouteFile)
	s.mux.HandleFunc("DELETE /api/files/{file_id}", s.handleDeleteFile)

	// Dynamic Node Onboarding & Registry
	s.mux.HandleFunc("POST /api/nodes", s.handleOnboardNode)
	s.mux.HandleFunc("GET /api/nodes", s.handleListNodes)
	s.mux.HandleFunc("DELETE /api/nodes/{node_id}", s.handleRemoveNode)
}

func (s *CentralServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	mongoOK := true
	if err := s.store.Client.Ping(ctx, nil); err != nil {
		mongoOK = false
	}

	currentNodes := s.getNodes()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"service":         "central-api",
		"status":          "healthy",
		"mongo_connected": mongoOK,
		"edge_nodes":      len(currentNodes),
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

	nodes := s.getNodes()
	for _, node := range nodes {
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
	s.replicator.DispatchAsync(&fileDoc, nodes)

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
	s.replicator.CascadeDelete(fileID, s.getNodes())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "deleted",
		"file_id": fileID,
	})
}

// OnboardNodeRequest represents payload to register a new edge storage node.
type OnboardNodeRequest struct {
	NodeID          string `json:"node_id"`
	Region          string `json:"region"`
	IP              string `json:"ip"`
	Port            string `json:"port"`
	Host            string `json:"host"`
	PublicURL       string `json:"public_url"`
	InternalURL     string `json:"internal_url"`
	SkipHealthCheck bool   `json:"skip_health_check"`
}

func (s *CentralServer) handleOnboardNode(w http.ResponseWriter, r *http.Request) {
	var req OnboardNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid json payload: %v", err), http.StatusBadRequest)
		return
	}

	if req.Region == "" {
		http.Error(w, "'region' is required (e.g. Asia, Europe, North America)", http.StatusBadRequest)
		return
	}
	if req.IP == "" && req.Host == "" && req.PublicURL == "" {
		http.Error(w, "'ip' or 'host' or 'public_url' is required", http.StatusBadRequest)
		return
	}

	port := req.Port
	if port == "" {
		port = "5001"
	}

	host := req.Host
	if host == "" {
		if req.IP != "" {
			host = fmt.Sprintf("%s:%s", req.IP, port)
		} else {
			host = "localhost:" + port
		}
	}

	publicURL := req.PublicURL
	if publicURL == "" {
		publicURL = fmt.Sprintf("http://%s", host)
	}

	internalURL := req.InternalURL
	if internalURL == "" {
		internalURL = publicURL
	}

	nodeID := req.NodeID
	if nodeID == "" {
		regionPrefix := strings.ToLower(req.Region)
		switch {
		case strings.Contains(regionPrefix, "asia") || strings.Contains(regionPrefix, "sg"):
			regionPrefix = "sg"
		case strings.Contains(regionPrefix, "america") || strings.Contains(regionPrefix, "us") || strings.Contains(regionPrefix, "ny"):
			regionPrefix = "ny"
		case strings.Contains(regionPrefix, "euro") || strings.Contains(regionPrefix, "ldn"):
			regionPrefix = "ldn"
		default:
			regionPrefix = "edge"
		}
		nodeID = fmt.Sprintf("%s-%d", regionPrefix, time.Now().Unix()%1000)
	}

	// Health probe (optional check)
	if !req.SkipHealthCheck {
		probeURL := fmt.Sprintf("%s/health", internalURL)
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(probeURL)
		if err != nil {
			log.Printf("[CentralAPI] Warning: Onboarding node %s at %s failed health probe: %v", nodeID, probeURL, err)
		} else {
			_ = resp.Body.Close()
		}
	}

	now := time.Now().UTC()
	newNode := models.NodeConfig{
		NodeID:      nodeID,
		Region:      req.Region,
		Host:        host,
		PublicURL:   publicURL,
		InternalURL: internalURL,
		Status:      "active",
		CreatedAt:   now,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// 1. Upsert into MongoDB nodes collection
	filter := bson.M{"node_id": nodeID}
	update := bson.M{"$set": newNode}
	_, err := s.nodesColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to save node to mongodb: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Update in-memory node list
	s.mu.Lock()
	found := false
	for i, n := range s.cfg.Nodes {
		if n.NodeID == nodeID {
			s.cfg.Nodes[i] = newNode
			found = true
			break
		}
	}
	if !found {
		s.cfg.Nodes = append(s.cfg.Nodes, newNode)
	}
	s.mu.Unlock()

	log.Printf("[CentralAPI] Successfully onboarded worker node [%s] in region [%s] (%s)", nodeID, req.Region, publicURL)

	// 3. Auto-backfill existing files to the new node in the background
	go s.backfillFilesToNode(newNode)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "onboarded",
		"node":       newNode,
		"message":    fmt.Sprintf("Worker node %s successfully registered in region %s", nodeID, req.Region),
		"persisted":  "mongodb://cdn_central.nodes",
		"timestamp":  now,
	})
}

func (s *CentralServer) backfillFilesToNode(node models.NodeConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cursor, err := s.filesColl.Find(ctx, bson.M{})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var files []models.FileDocument
	if err := cursor.All(ctx, &files); err != nil {
		return
	}

	log.Printf("[CentralAPI] Backfilling %d existing files to new node [%s]...", len(files), node.NodeID)
	for _, fileDoc := range files {
		replicaURL := fmt.Sprintf("%s/files/%s/%s", node.PublicURL, fileDoc.FileID, fileDoc.Filename)
		newReplica := models.ReplicaNode{
			NodeID: node.NodeID,
			Region: node.Region,
			Host:   node.Host,
			URL:    replicaURL,
			Status: models.StatusSyncing,
		}

		filter := bson.M{
			"file_id": fileDoc.FileID,
			"replicas.node_id": bson.M{"$ne": node.NodeID},
		}
		update := bson.M{
			"$push": bson.M{"replicas": newReplica},
		}
		_, _ = s.filesColl.UpdateOne(ctx, filter, update)

		// Stream file payload to new node
		s.replicator.ReplicateToSingleNode(&fileDoc, node)
	}
}

func (s *CentralServer) handleListNodes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := s.nodesColl.Find(ctx, bson.M{})
	var nodes []models.NodeConfig
	if err == nil {
		_ = cursor.All(ctx, &nodes)
	}
	if len(nodes) == 0 {
		nodes = s.getNodes()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

func (s *CentralServer) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		http.Error(w, "missing node_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err := s.nodesColl.DeleteOne(ctx, bson.M{"node_id": nodeID})
	if err != nil {
		http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	var updated []models.NodeConfig
	for _, n := range s.cfg.Nodes {
		if n.NodeID != nodeID {
			updated = append(updated, n)
		}
	}
	s.cfg.Nodes = updated
	s.mu.Unlock()

	log.Printf("[CentralAPI] Removed node [%s] from registry", nodeID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":  "removed",
		"node_id": nodeID,
	})
}
