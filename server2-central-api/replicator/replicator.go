package replicator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"mission1_cdn/pkg/models"
	"mission1_cdn/pkg/storage"
)

// Replicator orchestrates asynchronous background replication of files from Central MongoDB to edge storage nodes.
type Replicator struct {
	centralStore   *storage.GridFSStore
	filesColl      *mongo.Collection
	internalSecret string
	httpClient     *http.Client
}

// NewReplicator creates a new replication manager.
func NewReplicator(store *storage.GridFSStore, filesColl *mongo.Collection, internalSecret string) *Replicator {
	return &Replicator{
		centralStore:   store,
		filesColl:      filesColl,
		internalSecret: internalSecret,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// DispatchAsync initiates non-blocking background replication across all specified replica nodes.
func (r *Replicator) DispatchAsync(doc *models.FileDocument, nodes []models.NodeConfig) {
	go func() {
		for _, node := range nodes {
			// Run replication to each node concurrently
			go r.replicateToNode(doc, node)
		}
	}()
}

func (r *Replicator) replicateToNode(doc *models.FileDocument, node models.NodeConfig) {
	log.Printf("[Replicator] Replicating file %s (%s) to node %s (%s) at %s...", doc.FileID, doc.Filename, node.NodeID, node.Region, node.Host)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Download payload from Central GridFS
	downloadStream, err := r.centralStore.OpenDownloadStream(ctx, doc.FileID)
	if err != nil {
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, fmt.Sprintf("failed to read from central gridfs: %v", err))
		return
	}
	defer downloadStream.Close()

	// Prepare multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("file_id", doc.FileID)
	_ = writer.WriteField("filename", doc.Filename)
	_ = writer.WriteField("content_type", doc.ContentType)

	part, err := writer.CreateFormFile("file", doc.Filename)
	if err != nil {
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, fmt.Sprintf("failed to create form file: %v", err))
		return
	}

	if _, err := io.Copy(part, downloadStream); err != nil {
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, fmt.Sprintf("failed to stream file payload: %v", err))
		return
	}
	_ = writer.Close()

	// Send to edge storage node's /internal/replicate
	baseURL := node.InternalURL
	if baseURL == "" {
		baseURL = node.PublicURL
	}
	targetURL := fmt.Sprintf("%s/internal/replicate", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, body)
	if err != nil {
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, fmt.Sprintf("failed to create http request: %v", err))
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Internal-Secret", r.internalSecret)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		log.Printf("[Replicator] ERROR: Failed to replicate %s to %s: %v", doc.FileID, node.NodeID, err)
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		errStr := fmt.Sprintf("status code %d: %s", resp.StatusCode, string(respBody))
		log.Printf("[Replicator] ERROR: Node %s rejected replication of %s: %s", node.NodeID, doc.FileID, errStr)
		r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusFailed, errStr)
		return
	}

	log.Printf("[Replicator] SUCCESS: File %s successfully replicated to node %s (%s)", doc.FileID, node.NodeID, node.Region)
	r.updateReplicaStatus(doc.FileID, node.NodeID, models.StatusReady, "")
}

func (r *Replicator) updateReplicaStatus(fileID, nodeID, status, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC()
	filter := bson.M{
		"file_id":           fileID,
		"replicas.node_id": nodeID,
	}

	update := bson.M{
		"$set": bson.M{
			"replicas.$.status":        status,
			"replicas.$.synced_at":     now,
			"replicas.$.error_message": errMsg,
			"updated_at":               now,
		},
	}

	_, err := r.filesColl.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Printf("[Replicator] Failed to update replica status for %s on node %s in MongoDB: %v", fileID, nodeID, err)
	}
}

// CascadeDelete removes the file from all edge storage nodes concurrently.
func (r *Replicator) CascadeDelete(fileID string, nodes []models.NodeConfig) {
	for _, node := range nodes {
		go func(n models.NodeConfig) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			baseURL := n.InternalURL
			if baseURL == "" {
				baseURL = n.PublicURL
			}
			targetURL := fmt.Sprintf("%s/internal/files/%s", baseURL, fileID)
			req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
			if err != nil {
				return
			}
			req.Header.Set("X-Internal-Secret", r.internalSecret)
			resp, err := r.httpClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}(node)
	}
}
