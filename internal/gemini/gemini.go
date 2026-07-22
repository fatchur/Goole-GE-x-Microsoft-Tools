// Package gemini memanggil Gemini Enterprise API (streamAssist) memakai
// Google Access Token hasil tukar dari Workforce Identity Federation.
//
// Karena token yang dipakai adalah milik user yang login (bukan service
// account admin), hasil pencarian/jawaban otomatis dibatasi sesuai akses
// data yang dimiliki user itu di sumber data terhubung (mis. Outlook).
//
// IMPORTANT UPDATE (2026-07-12):
// Request format diupdate berdasarkan network capture analysis dari Gemini WebApp.
// Discovery krusial: "Blended search" default TIDAK otomatis include third-party
// connectors (seperti Outlook). Connector data stores HARUS dispesifikasikan
// eksplisit via toolsSpec.vertexAiSearchSpec.dataStoreSpecs.
//
// Dengan format request yang benar, manual authorization via Gemini WebApp
// CONFIRMED WORKING untuk custom apps yang menggunakan Workforce Identity
// Principal yang sama.
//
// PERFORMANCE UPDATE (2026-07-14):
// - Increased timeout to 180s for complex tasks (diagrams, image generation)
// - Added context support for better timeout control
// - Improved error messages with timeout details
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cngpt-bff-sso/internal/config"
)

type Client struct {
	cfg *config.Config
	hc  *http.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		hc: &http.Client{
			Timeout: 180 * time.Second, // Increased from 60s to 180s for complex tasks
		},
	}
}

// baseURL mengikuti aturan Gemini Enterprise: location "global" tidak
// memakai prefix pada hostname, location lain (us/eu) memakainya.
func (c *Client) baseURL() string {
	if c.cfg.GeminiLocation == "global" {
		return "https://discoveryengine.googleapis.com"
	}
	return fmt.Sprintf("https://%s-discoveryengine.googleapis.com", c.cfg.GeminiLocation)
}

// extractConnectorBaseID extracts base connector ID from various formats
// Examples:
//   - "collections/outlook-federated-connector_1783678287149/dataConnector"
//     -> "outlook-federated-connector_1783678287149"
//   - "outlook-federated-connector_1783678287149"
//     -> "outlook-federated-connector_1783678287149"
func extractConnectorBaseID(connectorID string) string {
	// If format is "collections/BASE_ID/dataConnector", extract BASE_ID
	if strings.Contains(connectorID, "/") {
		parts := strings.Split(connectorID, "/")
		for i, part := range parts {
			if part == "collections" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	// Otherwise, return as is (already base ID)
	return connectorID
}

type queryRequest struct {
	Query struct {
		Text   string       `json:"text,omitempty"`   // Simple text format for Discovery Engine
		Input  string       `json:"input,omitempty"`  // Alternative format for conversational API
		FileURIs []string   `json:"fileUris,omitempty"` // GCS URIs for file context
	} `json:"query"`
	Session *string  `json:"session,omitempty"` // Session ID for file context
	FileIDs []string `json:"fileIds,omitempty"` // File IDs uploaded to session (not used with GCS)
	UserLabels map[string]string `json:"userLabels,omitempty"` // Optional metadata
	ToolsSpec *struct {
		VertexAISearchSpec *struct {
			DataStoreSpecs []struct {
				DataStore string `json:"dataStore"`
			} `json:"dataStoreSpecs"`
		} `json:"vertexAiSearchSpec,omitempty"`
	} `json:"toolsSpec,omitempty"`
}

// addContextFileRequest is the request structure for uploading files to a session
type addContextFileRequest struct {
	Name         string `json:"name"`         // Full session resource name
	FileName     string `json:"fileName"`
	MimeType     string `json:"mimeType"`
	FileContents string `json:"fileContents"` // base64 encoded
}

// addContextFileResponse is the response from uploading a file
type addContextFileResponse struct {
	Session    string `json:"session"`
	FileID     string `json:"fileId"`
	TokenCount int    `json:"tokenCount,omitempty"`
}

// Ask mengirim satu pertanyaan ke default_assistant Gemini Enterprise
// (metode streamAssist) dan mengembalikan body respons mentah.
//
// IMPORTANT: Request format updated based on network capture analysis (2026-07-12).
// Discovery: "Blended search" default TIDAK auto-include third-party connectors.
// Connector data stores HARUS dispesifikasikan eksplisit via toolsSpec.
//
// Catatan: streamAssist sebenarnya bersifat streaming (Server-Sent Events).
// Untuk kesederhanaan latihan ini, kita baca seluruh body sekaligus
// (non-streaming). Untuk pengalaman chat yang responsif, respons ini
// idealnya di-relay ke frontend sebagai stream juga.
func (c *Client) Ask(googleAccessToken, questionText string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s/assistants/default_assistant:streamAssist",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Gemini Enterprise API Call:\n")
	fmt.Printf("  - URL: %s\n", url)
	fmt.Printf("  - Project: %s\n", c.cfg.GCPProjectNumber)
	fmt.Printf("  - Token preview: %s...\n", googleAccessToken[:50])

	var reqBody queryRequest

	// Use simple text format (Discovery Engine standard)
	reqBody.Query.Text = questionText

	// Add toolsSpec to explicitly include Outlook connector data stores
	// This is REQUIRED for connector data to be searchable
	if c.cfg.OutlookConnectorID != "" {
		baseID := extractConnectorBaseID(c.cfg.OutlookConnectorID)

		fmt.Printf("[DEBUG] Including Outlook connector data stores:\n")
		fmt.Printf("  - Base Connector ID: %s\n", baseID)

		reqBody.ToolsSpec = &struct {
			VertexAISearchSpec *struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			} `json:"vertexAiSearchSpec,omitempty"`
		}{
			VertexAISearchSpec: &struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			}{
				DataStoreSpecs: []struct {
					DataStore string `json:"dataStore"`
				}{
					// Mail data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail",
						c.cfg.GCPProjectNumber, baseID)},
					// Mail attachment data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment",
						c.cfg.GCPProjectNumber, baseID)},
					// Calendar data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar",
						c.cfg.GCPProjectNumber, baseID)},
					// Contact data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact",
						c.cfg.GCPProjectNumber, baseID)},
				},
			},
		}

		fmt.Printf("[DEBUG] Data stores included in request:\n")
		for i, ds := range reqBody.ToolsSpec.VertexAISearchSpec.DataStoreSpecs {
			fmt.Printf("  [%d] %s\n", i+1, ds.DataStore)
		}
	} else {
		fmt.Printf("[DEBUG] No Outlook connector configured, using default search only\n")
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	fmt.Printf("[DEBUG] Request body: %s\n", string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	// Wajib: quota project, lihat catatan di Bagian 6 dokumentasi.
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	startTime := time.Now()
	fmt.Printf("[DEBUG] 🕐 Request started at %s\n", startTime.Format("15:04:05"))

	resp, err := c.hc.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		// Enhanced error message with timing information
		if strings.Contains(err.Error(), "context deadline exceeded") ||
		   strings.Contains(err.Error(), "Client.Timeout") {
			return nil, fmt.Errorf("request timeout after %v (task too complex or API slow). Consider: (1) simplifying query, (2) retrying, or (3) contacting support if persists. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("request failed after %v: %w", elapsed.Round(time.Second), err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	elapsed := time.Since(startTime)

	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return nil, fmt.Errorf("response reading timeout after %v (API still generating response). This usually means the task is very complex. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("failed to read response after %v: %w", elapsed.Round(time.Second), err)
	}

	fmt.Printf("[DEBUG] ✅ Request completed in %v\n", elapsed.Round(time.Millisecond))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini enterprise API error (status %d) after %v: %s", resp.StatusCode, elapsed.Round(time.Second), string(respBytes))
	}

	return respBytes, nil
}

// AskWithContext is like Ask but accepts a context for better timeout control
// This allows per-request timeout customization
func (c *Client) AskWithContext(ctx context.Context, googleAccessToken, questionText string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s/assistants/default_assistant:streamAssist",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Gemini Enterprise API Call (with context):\n")
	fmt.Printf("  - URL: %s\n", url)

	var reqBody queryRequest
	reqBody.Query.Text = questionText

	// Add toolsSpec if connector configured
	if c.cfg.OutlookConnectorID != "" {
		baseID := extractConnectorBaseID(c.cfg.OutlookConnectorID)
		reqBody.ToolsSpec = &struct {
			VertexAISearchSpec *struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			} `json:"vertexAiSearchSpec,omitempty"`
		}{
			VertexAISearchSpec: &struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			}{
				DataStoreSpecs: []struct {
					DataStore string `json:"dataStore"`
				}{
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact", c.cfg.GCPProjectNumber, baseID)},
				},
			},
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	startTime := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("request timeout after %v: task too complex or API slow", elapsed.Round(time.Second))
		}
		return nil, fmt.Errorf("request failed after %v: %w", elapsed.Round(time.Second), err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	elapsed := time.Since(startTime)

	if err != nil {
		return nil, fmt.Errorf("failed to read response after %v: %w", elapsed.Round(time.Second), err)
	}

	fmt.Printf("[DEBUG] ✅ Request completed in %v\n", elapsed.Round(time.Millisecond))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini enterprise API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// AddContextFile uploads a file to a session for use in subsequent queries
// Returns the session ID and file ID that can be used in AskWithFiles
func (c *Client) AddContextFile(googleAccessToken, sessionID, fileName, mimeType string, fileData []byte) (string, string, error) {
	// If no session ID provided, use "-" to auto-generate
	var sessionPath string
	if sessionID == "" {
		// Use "-" for auto-generated session ID
		sessionPath = fmt.Sprintf("projects/%s/locations/%s/collections/default_collection/engines/%s/sessions/-",
			c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID)
	} else {
		// Use provided session ID (should be full path)
		sessionPath = sessionID
	}

	url := fmt.Sprintf("%s/v1beta/%s:addContextFile", c.baseURL(), sessionPath)

	fmt.Printf("[DEBUG] Adding file to session:\n")
	fmt.Printf("  - Session path: %s\n", sessionPath)
	fmt.Printf("  - File name: %s\n", fileName)
	fmt.Printf("  - MIME type: %s\n", mimeType)
	fmt.Printf("  - File size: %d bytes (%.2f KB)\n", len(fileData), float64(len(fileData))/1024)

	// Encode file to base64
	fileDataBase64 := base64.StdEncoding.EncodeToString(fileData)

	reqBody := addContextFileRequest{
		Name:         sessionPath, // REQUIRED: Full session resource name
		FileName:     fileName,
		MimeType:     mimeType,
		FileContents: fileDataBase64,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	startTime := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	elapsed := time.Since(startTime)

	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var response addContextFileResponse
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Printf("[DEBUG] ✅ File uploaded successfully in %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  - File ID: %s\n", response.FileID)
	fmt.Printf("  - Token count: %d\n", response.TokenCount)

	return response.Session, response.FileID, nil
}

// AskWithFiles sends a query with file context
// sessionID and fileIDs should be obtained from AddContextFile
func (c *Client) AskWithFiles(googleAccessToken, sessionID string, fileIDs []string, questionText string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s/assistants/default_assistant:streamAssist",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Gemini Enterprise API Call with Files:\n")
	fmt.Printf("  - Session: %s\n", sessionID)
	fmt.Printf("  - File IDs: %v\n", fileIDs)
	fmt.Printf("  - Question: %s\n", questionText)

	var reqBody queryRequest
	reqBody.Query.Text = questionText
	reqBody.Session = &sessionID
	reqBody.FileIDs = fileIDs

	// Add toolsSpec if connector configured
	if c.cfg.OutlookConnectorID != "" {
		baseID := extractConnectorBaseID(c.cfg.OutlookConnectorID)
		reqBody.ToolsSpec = &struct {
			VertexAISearchSpec *struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			} `json:"vertexAiSearchSpec,omitempty"`
		}{
			VertexAISearchSpec: &struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			}{
				DataStoreSpecs: []struct {
					DataStore string `json:"dataStore"`
				}{
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact", c.cfg.GCPProjectNumber, baseID)},
				},
			},
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	startTime := time.Now()
	fmt.Printf("[DEBUG] 🕐 Request with files started at %s\n", startTime.Format("15:04:05"))

	resp, err := c.hc.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		if strings.Contains(err.Error(), "context deadline exceeded") ||
			strings.Contains(err.Error(), "Client.Timeout") {
			return nil, fmt.Errorf("request timeout after %v (task too complex or API slow). Consider: (1) simplifying query, (2) retrying, or (3) contacting support if persists. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("request failed after %v: %w", elapsed.Round(time.Second), err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	elapsed := time.Since(startTime)

	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return nil, fmt.Errorf("response reading timeout after %v (API still generating response). This usually means the task is very complex. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("failed to read response after %v: %w", elapsed.Round(time.Second), err)
	}

	fmt.Printf("[DEBUG] ✅ Request with files completed in %v\n", elapsed.Round(time.Millisecond))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini enterprise API error (status %d) after %v: %s", resp.StatusCode, elapsed.Round(time.Second), string(respBytes))
	}

	return respBytes, nil
}

// AskWithGCSFiles sends a query to Gemini Enterprise with GCS file URIs
// gcsURIs should be in format: gs://bucket-name/path/to/file.pdf
func (c *Client) AskWithGCSFiles(googleAccessToken string, gcsURIs []string, questionText string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s/assistants/default_assistant:streamAssist",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Gemini Enterprise API Call with GCS Files:\n")
	fmt.Printf("  - GCS URIs: %v\n", gcsURIs)
	fmt.Printf("  - Question: %s\n", questionText)

	var reqBody queryRequest

	// Build prompt that references the GCS files
	// NOTE: Discovery Engine streamAssist does not natively support file attachments
	// This is a limitation - files need to be imported to Data Store or use Vertex AI Gemini API instead
	fullPrompt := questionText
	if len(gcsURIs) > 0 {
		fullPrompt = fmt.Sprintf("%s\n\n[Context: Uploaded files at %s]", questionText, strings.Join(gcsURIs, ", "))
		fmt.Printf("[DEBUG] ⚠️  Note: Discovery Engine may not be able to read GCS files directly\n")
		fmt.Printf("[DEBUG] Files should be imported to Data Store or use Vertex AI Gemini API\n")
	}

	reqBody.Query.Text = fullPrompt

	// Add toolsSpec if connector configured
	if c.cfg.OutlookConnectorID != "" {
		baseID := extractConnectorBaseID(c.cfg.OutlookConnectorID)
		reqBody.ToolsSpec = &struct {
			VertexAISearchSpec *struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			} `json:"vertexAiSearchSpec,omitempty"`
		}{
			VertexAISearchSpec: &struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			}{
				DataStoreSpecs: []struct {
					DataStore string `json:"dataStore"`
				}{
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar", c.cfg.GCPProjectNumber, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact", c.cfg.GCPProjectNumber, baseID)},
				},
			},
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	fmt.Printf("[DEBUG] Request body: %s\n", string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	startTime := time.Now()
	fmt.Printf("[DEBUG] 🕐 Request with GCS files started at %s\n", startTime.Format("15:04:05"))

	resp, err := c.hc.Do(req)
	if err != nil {
		elapsed := time.Since(startTime)
		if strings.Contains(err.Error(), "context deadline exceeded") ||
			strings.Contains(err.Error(), "Client.Timeout") {
			return nil, fmt.Errorf("request timeout after %v (task too complex or API slow). Consider: (1) simplifying query, (2) retrying, or (3) contacting support if persists. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("request failed after %v: %w", elapsed.Round(time.Second), err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	elapsed := time.Since(startTime)

	if err != nil {
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return nil, fmt.Errorf("response reading timeout after %v (API still generating response). This usually means the task is very complex. Original error: %w", elapsed.Round(time.Second), err)
		}
		return nil, fmt.Errorf("failed to read response after %v: %w", elapsed.Round(time.Second), err)
	}

	fmt.Printf("[DEBUG] ✅ Request with GCS files completed in %v\n", elapsed.Round(time.Millisecond))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini enterprise API error (status %d) after %v: %s", resp.StatusCode, elapsed.Round(time.Second), string(respBytes))
	}

	return respBytes, nil
}

// AcquireAndStoreRefreshToken uses Discovery Engine API to exchange OAuth authorization code
// and store the refresh token for connector access
func (c *Client) AcquireAndStoreRefreshToken(googleAccessToken, connectorID, fullRedirectURI string) error {
	// Build correct endpoint path per Discovery Engine API spec (corrected based on Google's official response)
	// Opsi B: Engine-scoped path (with engine context)
	// Format: /v1alpha/projects/{PROJECT}/locations/global/collections/default_collection/engines/{ENGINE_ID}/dataConnectors/{CONNECTOR_ID}:acquireAndStoreRefreshToken
	dataConnectorPath := fmt.Sprintf("projects/%s/locations/global/collections/default_collection/engines/%s/dataConnectors/%s",
		c.cfg.GCPProjectNumber,
		c.cfg.GeminiAppID, // ENGINE_ID
		connectorID,       // CONNECTOR_ID in URL path
	)
	url := fmt.Sprintf("%s/v1alpha/%s:acquireAndStoreRefreshToken", c.baseURL(), dataConnectorPath)

	fmt.Printf("[DEBUG] Acquiring and storing connector refresh token:\n")
	fmt.Printf("  - URL: %s\n", url)
	fmt.Printf("  - Project ID: %s\n", c.cfg.GCPProjectNumber)
	fmt.Printf("  - Engine ID: %s\n", c.cfg.GeminiAppID)
	fmt.Printf("  - Connector ID: %s\n", connectorID)
	fmt.Printf("  - Full Redirect URI: %s\n", fullRedirectURI)

	// Build payload per Discovery Engine API spec
	// When connector ID is in URL path, payload only needs fullRedirectUri
	payload := map[string]any{
		"fullRedirectUri": fullRedirectURI,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	fmt.Printf("[DEBUG] Request payload: %s\n", string(bodyBytes))

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Printf("[DEBUG] Response (status %d): %s\n", resp.StatusCode, string(respBytes))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("failed to acquire and store refresh token (status %d): %s", resp.StatusCode, string(respBytes))
	}

	fmt.Printf("[DEBUG] ✅ Connector refresh token acquired and stored successfully\n")
	return nil
}
