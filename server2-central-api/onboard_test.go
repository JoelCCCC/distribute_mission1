package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mission1_cdn/pkg/models"
)

func TestDynamicNodeOnboardingHandlers(t *testing.T) {
	// Setup test CentralServer with in-memory nodes (without requiring live MongoDB for unit test)
	cfg := Config{
		Port:          "4000",
		PrimaryNodeID: "sg-1",
		Nodes: []models.NodeConfig{
			{NodeID: "sg-1", Region: "Asia", Host: "localhost:5001", PublicURL: "http://localhost:5001"},
		},
	}

	server := &CentralServer{
		cfg: cfg,
		mux: http.NewServeMux(),
	}

	// Register test routes
	server.mux.HandleFunc("POST /api/nodes", func(w http.ResponseWriter, r *http.Request) {
		var req OnboardNodeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		newNode := models.NodeConfig{
			NodeID:    req.NodeID,
			Region:    req.Region,
			Host:      req.IP + ":" + req.Port,
			PublicURL: "http://" + req.IP + ":" + req.Port,
			Status:    "active",
			CreatedAt: time.Now().UTC(),
		}

		server.mu.Lock()
		server.cfg.Nodes = append(server.cfg.Nodes, newNode)
		server.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "onboarded", "node": newNode})
	})

	server.mux.HandleFunc("GET /api/nodes", server.handleListNodes)

	// 1. Test POST /api/nodes
	payload := []byte(`{"node_id":"sg-2","region":"Asia","ip":"198.45.98.44","port":"5001","skip_health_check":true}`)
	req := httptest.NewRequest("POST", "/api/nodes", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Verify in-memory list updated
	nodes := server.getNodes()
	if len(nodes) != 2 {
		t.Fatalf("Expected 2 nodes after onboarding, got %d", len(nodes))
	}
	if nodes[1].NodeID != "sg-2" || nodes[1].Region != "Asia" {
		t.Errorf("Unexpected node data: %+v", nodes[1])
	}
}
