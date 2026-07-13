// Package gemini memanggil Gemini Enterprise API (streamAssist) memakai
// Google Access Token hasil tukar dari Workforce Identity Federation.
//
// Karena token yang dipakai adalah milik user yang login (bukan service
// account admin), hasil pencarian/jawaban otomatis dibatasi sesuai akses
// data yang dimiliki user itu di sumber data terhubung (mis. Outlook).
package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

type queryRequest struct {
	Query struct {
		Text string `json:"text"`
	} `json:"query"`
}

// Ask mengirim satu pertanyaan ke default_assistant Gemini Enterprise
// (metode streamAssist) dan mengembalikan body respons mentah.
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
	reqBody.Query.Text = questionText
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
