package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"mission1_cdn/pkg/models"
)

func TestFileDocumentSerialization(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	doc := models.FileDocument{
		FileID:      "img_98765",
		Filename:    "banner.png",
		ContentType: "image/png",
		SizeBytes:   245120,
		URLForward:  "http://198.45.98.44/files/img_98765/banner.png",
		CreatedAt:   now,
		UpdatedAt:   now,
		Replicas: []models.ReplicaNode{
			{
				NodeID: "sg-1",
				Region: "Asia",
				Host:   "198.45.98.44",
				URL:    "http://198.45.98.44/files/img_98765/banner.png",
				Status: models.StatusReady,
			},
			{
				NodeID: "ny-1",
				Region: "North America",
				Host:   "54.210.12.33",
				URL:    "http://54.210.12.33/files/img_98765/banner.png",
				Status: models.StatusReady,
			},
			{
				NodeID: "ldn-1",
				Region: "Europe",
				Host:   "18.130.45.67",
				URL:    "http://18.130.45.67/files/img_98765/banner.png",
				Status: models.StatusReady,
			},
		},
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Failed to marshal FileDocument: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal into map: %v", err)
	}

	// Verify required fields match user specification
	if parsed["file_id"] != "img_98765" {
		t.Errorf("Expected file_id img_98765, got %v", parsed["file_id"])
	}
	if parsed["filename"] != "banner.png" {
		t.Errorf("Expected filename banner.png, got %v", parsed["filename"])
	}
	if parsed["url_forward"] != "http://198.45.98.44/files/img_98765/banner.png" {
		t.Errorf("Expected url_forward http://198.45.98.44/files/img_98765/banner.png, got %v", parsed["url_forward"])
	}

	replicas, ok := parsed["replicas"].([]any)
	if !ok || len(replicas) != 3 {
		t.Fatalf("Expected 3 replicas, got %v", replicas)
	}

	firstReplica := replicas[0].(map[string]any)
	if firstReplica["node_id"] != "sg-1" {
		t.Errorf("Expected sg-1, got %v", firstReplica["node_id"])
	}
	if firstReplica["region"] != "Asia" {
		t.Errorf("Expected Asia, got %v", firstReplica["region"])
	}
	if firstReplica["url"] != "http://198.45.98.44/files/img_98765/banner.png" {
		t.Errorf("Expected Singapore URL, got %v", firstReplica["url"])
	}
	if firstReplica["status"] != "ready" {
		t.Errorf("Expected ready status, got %v", firstReplica["status"])
	}
}
