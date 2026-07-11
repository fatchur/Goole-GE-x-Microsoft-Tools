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
