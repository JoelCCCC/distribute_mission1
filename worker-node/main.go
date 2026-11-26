package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"mission1_cdn/pkg/edge"
	"mission1_cdn/pkg/storage"
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	cfg := edge.Config{
		NodeID:         getEnv("NODE_ID", "worker-1"),
		Region:         getEnv("REGION", "Global"),
		Port:           getEnv("PORT", "5001"),
		PublicURL:      getEnv("PUBLIC_URL", "http://localhost:5001"),
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:         getEnv("DB_NAME", "cdn_storage"),
		InternalSecret: getEnv("INTERNAL_SECRET", "cdn-super-secret-key-2026"),
	}

	log.Printf("Starting Generic CDN Edge Storage Node [%s] in Region [%s] on port %s...", cfg.NodeID, cfg.Region, cfg.Port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := storage.NewGridFSStore(ctx, cfg.MongoURI, cfg.DBName)
	if err != nil {
		log.Fatalf("Failed to initialize MongoDB GridFS store at %s: %v", cfg.MongoURI, err)
	}
	defer store.Close(context.Background())

	server := edge.NewServer(cfg, store)

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      server.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Printf("Worker Edge Storage Node [%s] is serving at %s (Public URL: %s)", cfg.NodeID, httpServer.Addr, cfg.PublicURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
