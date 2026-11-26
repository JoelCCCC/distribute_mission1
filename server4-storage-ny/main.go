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
		NodeID:         getEnv("NODE_ID", "ny-1"),
		Region:         getEnv("REGION", "North America"),
		Port:           getEnv("PORT", "5002"),
		PublicURL:      getEnv("PUBLIC_URL", "http://localhost:5002"),
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27019"),
		DBName:         getEnv("DB_NAME", "cdn_storage_ny"),
		InternalSecret: getEnv("INTERNAL_SECRET", "cdn-super-secret-key-2026"),
	}

	log.Printf("Starting Storage Edge Node [%s] in Region [%s] on port %s...", cfg.NodeID, cfg.Region, cfg.Port)

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

	log.Printf("Storage Edge Node [%s] is serving at %s (Public URL: %s)", cfg.NodeID, httpServer.Addr, cfg.PublicURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
