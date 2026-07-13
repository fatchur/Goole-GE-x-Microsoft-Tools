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
package gemini

import (
	"bytes"
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
	return &Client{cfg: cfg, hc: &http.Client{Timeout: 60 * time.Second}}
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
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"query"`
	ToolsSpec *struct {
		VertexAISearchSpec *struct {
			DataStoreSpecs []struct {
				DataStore string `json:"dataStore"`
			} `json:"dataStoreSpecs"`
		} `json:"vertexAiSearchSpec,omitempty"`
	} `json:"toolsSpec,omitempty"`
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
		c.baseURL(), c.cfg.GCPProjectID, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Gemini Enterprise API Call:\n")
	fmt.Printf("  - URL: %s\n", url)
	fmt.Printf("  - Project: %s\n", c.cfg.GCPProjectID)
	fmt.Printf("  - Token preview: %s...\n", googleAccessToken[:50])

	var reqBody queryRequest

	// Use "parts" array format (required format from network capture)
	reqBody.Query.Parts = []struct {
		Text string `json:"text"`
	}{
		{Text: questionText},
	}

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
						c.cfg.GCPProjectID, baseID)},
					// Mail attachment data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment",
						c.cfg.GCPProjectID, baseID)},
					// Calendar data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar",
						c.cfg.GCPProjectID, baseID)},
					// Contact data store
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact",
						c.cfg.GCPProjectID, baseID)},
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
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectID)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini enterprise API error (status %d): %s", resp.StatusCode, string(respBytes))
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
		c.cfg.GCPProjectID,
		c.cfg.GeminiAppID, // ENGINE_ID
		connectorID,       // CONNECTOR_ID in URL path
	)
	url := fmt.Sprintf("%s/v1alpha/%s:acquireAndStoreRefreshToken", c.baseURL(), dataConnectorPath)

	fmt.Printf("[DEBUG] Acquiring and storing connector refresh token:\n")
	fmt.Printf("  - URL: %s\n", url)
	fmt.Printf("  - Project ID: %s\n", c.cfg.GCPProjectID)
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
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectID)

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
