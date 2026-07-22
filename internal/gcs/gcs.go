// Package gcs provides utilities for uploading files to Google Cloud Storage
package gcs

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"cngpt-bff-sso/internal/config"
)

type Client struct {
	cfg    *config.Config
	client *storage.Client
}

func NewClient(cfg *config.Config) (*Client, error) {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &Client{
		cfg:    cfg,
		client: client,
	}, nil
}

func (c *Client) Close() error {
	return c.client.Close()
}

// UploadFile uploads a file to GCS and returns the gs:// URI
// fileName akan di-sanitize untuk menghindari masalah dengan karakter special
func (c *Client) UploadFile(ctx context.Context, fileName string, mimeType string, data io.Reader) (string, error) {
	// Sanitize filename: replace spaces and special chars
	sanitizedName := sanitizeFilename(fileName)

	// Add timestamp prefix to ensure uniqueness
	timestamp := time.Now().Format("20060102-150405")
	objectName := fmt.Sprintf("uploads/%s-%s", timestamp, sanitizedName)

	fmt.Printf("[DEBUG] Uploading file to GCS:\n")
	fmt.Printf("  - Bucket: %s\n", c.cfg.GCSBucketName)
	fmt.Printf("  - Object: %s\n", objectName)
	fmt.Printf("  - MIME type: %s\n", mimeType)

	// Create GCS object writer
	bucket := c.client.Bucket(c.cfg.GCSBucketName)
	obj := bucket.Object(objectName)
	writer := obj.NewWriter(ctx)
	writer.ContentType = mimeType

	// Copy data to GCS
	bytesWritten, err := io.Copy(writer, data)
	if err != nil {
		return "", fmt.Errorf("failed to write file to GCS: %w", err)
	}

	// Close the writer to finalize upload
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize GCS upload: %w", err)
	}

	gcsURI := fmt.Sprintf("gs://%s/%s", c.cfg.GCSBucketName, objectName)
	fmt.Printf("[DEBUG] ✅ File uploaded successfully: %s (%d bytes)\n", gcsURI, bytesWritten)

	return gcsURI, nil
}

// sanitizeFilename removes or replaces characters that might cause issues in GCS paths
func sanitizeFilename(name string) string {
	// Remove extension first
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)

	// Replace problematic characters
	replacer := strings.NewReplacer(
		" ", "_",
		"[", "",
		"]", "",
		"(", "",
		")", "",
		"{", "",
		"}", "",
		"#", "",
		"%", "",
		"&", "",
		"*", "",
		"!", "",
		"@", "",
		"$", "",
		"'", "",
		"\"", "",
		"`", "",
		"<", "",
		">", "",
		"|", "",
		":", "",
		";", "",
		"?", "",
	)

	cleanBase := replacer.Replace(base)

	// Remove multiple consecutive underscores
	for strings.Contains(cleanBase, "__") {
		cleanBase = strings.ReplaceAll(cleanBase, "__", "_")
	}

	// Trim underscores from start/end
	cleanBase = strings.Trim(cleanBase, "_")

	return cleanBase + ext
}
