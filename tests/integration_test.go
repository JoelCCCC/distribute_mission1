package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mission1_cdn/pkg/models"
	"mission1_cdn/server1-webapp/geo"
)

// MockEdgeNode simulates an edge storage server's HTTP endpoints.
type MockEdgeNode struct {
	nodeID    string
	region    string
	secret    string
	files     map[string][]byte
	server    *httptest.Server
	mu        sync.RWMutex
	replicateCount int
}

func newMockEdgeNode(nodeID, region, secret string) *MockEdgeNode {
	m := &MockEdgeNode{
		nodeID: nodeID,
		region: region,
		secret: secret,
		files:  make(map[string][]byte),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/replicate", m.handleReplicate)
	mux.HandleFunc("GET /files/{file_id}/{filename}", m.handleServe)
	mux.HandleFunc("GET /health", m.handleHealth)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockEdgeNode) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"node_id": m.nodeID,
		"region":  m.region,
		"status":  "healthy",
	})
}

func (m *MockEdgeNode) handleReplicate(w http.ResponseWriter, r *http.Request) {
	if secret := r.Header.Get("X-Internal-Secret"); secret != m.secret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	_ = r.ParseMultipartForm(10 << 20)
	fileID := r.FormValue("file_id")
	filename := r.FormValue("filename")
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, _ := io.ReadAll(file)
	m.mu.Lock()
	m.files[fileID] = data
	m.replicateCount++
	m.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ready",
		"node_id":  m.nodeID,
		"file_id":  fileID,
		"filename": filename,
		"url":      fmt.Sprintf("%s/files/%s/%s", m.server.URL, fileID, filename),
	})
}

func (m *MockEdgeNode) handleServe(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	m.mu.RLock()
	data, exists := m.files[fileID]
	m.mu.RUnlock()

	if !exists {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (m *MockEdgeNode) Close() {
	m.server.Close()
}

func TestEdgeReplicationAndGeoRouting(t *testing.T) {
	const secret = "test-cdn-secret-2026"

	// 1. Start mock edge nodes
	sgNode := newMockEdgeNode("sg-1", "Asia", secret)
	defer sgNode.Close()

	nyNode := newMockEdgeNode("ny-1", "North America", secret)
	defer nyNode.Close()

	ldnNode := newMockEdgeNode("ldn-1", "Europe", secret)
	defer ldnNode.Close()

	// 2. Prepare sample file
	samplePayload := []byte("fake-banner-png-content-for-cdn-verification")
	fileID := "img_98765"
	filename := "banner.png"

	// 3. Test replication directly to all 3 nodes
	nodes := []*MockEdgeNode{sgNode, nyNode, ldnNode}
	for _, node := range nodes {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.WriteField("file_id", fileID)
		_ = writer.WriteField("filename", filename)
		_ = writer.WriteField("content_type", "image/png")
		part, _ := writer.CreateFormFile("file", filename)
		_, _ = part.Write(samplePayload)
		_ = writer.Close()

		req, _ := http.NewRequest("POST", node.server.URL+"/internal/replicate", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("X-Internal-Secret", secret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to replicate to %s: %v", node.nodeID, err)
		}
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("Expected status 201 from %s, got %d", node.nodeID, resp.StatusCode)
		}
		_ = resp.Body.Close()

		// Verify edge node received and stored the file
		node.mu.RLock()
		stored, exists := node.files[fileID]
		node.mu.RUnlock()

		if !exists || string(stored) != string(samplePayload) {
			t.Fatalf("Node %s did not store expected file bytes", node.nodeID)
		}

		// Verify direct download from edge
		getResp, err := http.Get(fmt.Sprintf("%s/files/%s/%s", node.server.URL, fileID, filename))
		if err != nil {
			t.Fatalf("Failed to fetch from edge %s: %v", node.nodeID, err)
		}
		downloaded, _ := io.ReadAll(getResp.Body)
		_ = getResp.Body.Close()
		if string(downloaded) != string(samplePayload) {
			t.Fatalf("Fetched bytes mismatch from edge %s", node.nodeID)
		}
	}

	// 4. Test Geo-Routing simulation logic
	now := time.Now().UTC()
	fileDoc := models.FileDocument{
		FileID:     fileID,
		Filename:   filename,
		SizeBytes:  int64(len(samplePayload)),
		URLForward: fmt.Sprintf("%s/files/%s/%s", sgNode.server.URL, fileID, filename),
		CreatedAt:  now,
		UpdatedAt:  now,
		Replicas: []models.ReplicaNode{
			{NodeID: "sg-1", Region: "Asia", URL: fmt.Sprintf("%s/files/%s/%s", sgNode.server.URL, fileID, filename), Status: models.StatusReady},
			{NodeID: "ny-1", Region: "North America", URL: fmt.Sprintf("%s/files/%s/%s", nyNode.server.URL, fileID, filename), Status: models.StatusReady},
			{NodeID: "ldn-1", Region: "Europe", URL: fmt.Sprintf("%s/files/%s/%s", ldnNode.server.URL, fileID, filename), Status: models.StatusReady},
		},
	}

	// Simulate Client from Asia
	reqAsia := httptest.NewRequest("GET", fmt.Sprintf("/cdn/%s/%s?region=Asia", fileID, filename), nil)
	regionAsia := geo.DetectClientRegion(reqAsia)
	var routedURL string
	for _, rep := range fileDoc.Replicas {
		if strings.EqualFold(rep.Region, regionAsia.Region) {
			routedURL = rep.URL
			break
		}
	}
	if !strings.HasPrefix(routedURL, sgNode.server.URL) {
		t.Errorf("Expected Asia request to route to Singapore node (%s), got %s", sgNode.server.URL, routedURL)
	}

	// Simulate Client from Europe
	reqEU := httptest.NewRequest("GET", fmt.Sprintf("/cdn/%s/%s?region=Europe", fileID, filename), nil)
	regionEU := geo.DetectClientRegion(reqEU)
	for _, rep := range fileDoc.Replicas {
		if strings.EqualFold(rep.Region, regionEU.Region) {
			routedURL = rep.URL
			break
		}
	}
	if !strings.HasPrefix(routedURL, ldnNode.server.URL) {
		t.Errorf("Expected Europe request to route to London node (%s), got %s", ldnNode.server.URL, routedURL)
	}

	// Simulate Client from North America
	reqNA := httptest.NewRequest("GET", fmt.Sprintf("/cdn/%s/%s?region=North%%20America", fileID, filename), nil)
	regionNA := geo.DetectClientRegion(reqNA)
	for _, rep := range fileDoc.Replicas {
		if strings.EqualFold(rep.Region, regionNA.Region) {
			routedURL = rep.URL
			break
		}
	}
	if !strings.HasPrefix(routedURL, nyNode.server.URL) {
		t.Errorf("Expected North America request to route to New York node (%s), got %s", nyNode.server.URL, routedURL)
	}
}
