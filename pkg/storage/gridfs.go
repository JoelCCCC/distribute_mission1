package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/gridfs"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GridFSStore wraps a MongoDB Client, Database, and GridFS Bucket.
type GridFSStore struct {
	Client *mongo.Client
	DB     *mongo.Database
	Bucket *gridfs.Bucket
}

// NewGridFSStore connects to MongoDB and initializes the GridFS bucket.
func NewGridFSStore(ctx context.Context, uri, dbName string) (*GridFSStore, error) {
	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb at %s: %w", uri, err)
	}

	// Ping to verify connection
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("failed to ping mongodb at %s: %w", uri, err)
	}

	db := client.Database(dbName)
	bucketOpts := options.GridFSBucket().SetName("fs")
	bucket, err := gridfs.NewBucket(db, bucketOpts)
	if err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("failed to initialize gridfs bucket: %w", err)
	}

	return &GridFSStore{
		Client: client,
		DB:     db,
		Bucket: bucket,
	}, nil
}

// Upload streams data from reader into MongoDB GridFS, associating it with fileID as the filename or metadata.
func (s *GridFSStore) Upload(ctx context.Context, fileID, filename, contentType string, reader io.Reader) (int64, error) {
	uploadOpts := options.GridFSUpload().SetMetadata(bson.M{
		"file_id":      fileID,
		"filename":     filename,
		"content_type": contentType,
		"uploaded_at":  time.Now().UTC(),
	})

	uploadStream, err := s.Bucket.OpenUploadStreamWithID(fileID, filename, uploadOpts)
	if err != nil {
		return 0, fmt.Errorf("failed to open gridfs upload stream for %s: %w", fileID, err)
	}
	defer uploadStream.Close()

	written, err := io.Copy(uploadStream, reader)
	if err != nil {
		return 0, fmt.Errorf("failed to stream content into gridfs for %s: %w", fileID, err)
	}

	return written, nil
}

// OpenDownloadStream opens a read stream for the file with the given fileID.
func (s *GridFSStore) OpenDownloadStream(ctx context.Context, fileID string) (*gridfs.DownloadStream, error) {
	stream, err := s.Bucket.OpenDownloadStream(fileID)
	if err != nil {
		return nil, fmt.Errorf("failed to open gridfs download stream for %s: %w", fileID, err)
	}
	return stream, nil
}

// Download streams the file with fileID directly to the provided writer.
func (s *GridFSStore) Download(ctx context.Context, fileID string, writer io.Writer) error {
	_, err := s.Bucket.DownloadToStream(fileID, writer)
	if err != nil {
		return fmt.Errorf("failed to download %s from gridfs: %w", fileID, err)
	}
	return nil
}

// Delete removes a file and its chunks from GridFS.
func (s *GridFSStore) Delete(ctx context.Context, fileID string) error {
	err := s.Bucket.Delete(fileID)
	if err != nil && err != gridfs.ErrFileNotFound {
		return fmt.Errorf("failed to delete %s from gridfs: %w", fileID, err)
	}
	return nil
}

// Exists checks if a file with the given fileID exists in GridFS.
func (s *GridFSStore) Exists(ctx context.Context, fileID string) (bool, error) {
	filter := bson.M{"_id": fileID}
	cursor, err := s.Bucket.Find(filter)
	if err != nil {
		return false, err
	}
	defer cursor.Close(ctx)
	return cursor.Next(ctx), nil
}

// Close gracefully closes the MongoDB connection.
func (s *GridFSStore) Close(ctx context.Context) error {
	return s.Client.Disconnect(ctx)
}
