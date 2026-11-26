package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ReplicaStatus constants
const (
	StatusSyncing = "syncing"
	StatusReady   = "ready"
	StatusFailed  = "failed"
)

// ReplicaNode represents the replication status and access URL for a specific edge storage server.
type ReplicaNode struct {
	NodeID       string     `bson:"node_id" json:"node_id"`
	Region       string     `bson:"region" json:"region"`
	Host         string     `bson:"host" json:"host"`
	URL          string     `bson:"url" json:"url"`
	Status       string     `bson:"status" json:"status"`
	SyncedAt     *time.Time `bson:"synced_at,omitempty" json:"synced_at,omitempty"`
	ErrorMessage string     `bson:"error_message,omitempty" json:"error_message,omitempty"`
}

// FileDocument is the MongoDB document schema representing an uploaded file across Central API and storage nodes.
type FileDocument struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"_id,omitempty"`
	FileID      string             `bson:"file_id" json:"file_id"`
	Filename    string             `bson:"filename" json:"filename"`
	ContentType string             `bson:"content_type" json:"content_type"`
	SizeBytes   int64              `bson:"size_bytes" json:"size_bytes"`
	SHA256      string             `bson:"sha256,omitempty" json:"sha256,omitempty"`
	URLForward  string             `bson:"url_forward" json:"url_forward"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
	Replicas    []ReplicaNode      `bson:"replicas" json:"replicas"`
}

// NodeConfig represents an edge node definition in configuration.
type NodeConfig struct {
	NodeID      string `json:"node_id"`
	Region      string `json:"region"`
	Host        string `json:"host"`                   // Public or internal host
	PublicURL   string `json:"public_url"`             // Public URL for clients e.g. http://198.45.98.44:5001
	InternalURL string `json:"internal_url,omitempty"` // Internal replication URL for Server 2 (e.g. http://server3-storage-sg:5001)
}
