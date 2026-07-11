// Package gcpsts menukar ID Token Microsoft menjadi Google Access Token,
// lewat Google Security Token Service (STS) — inilah inti dari Workforce
// Identity Federation yang sudah dikonfigurasi di GCP (Bagian 2-3 dokumentasi).
package gcpsts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cngpt-bff-sso/internal/config"
)

const stsEndpoint = "https://sts.googleapis.com/v1/token"

type Client struct {
	cfg *config.Config
	hc  *http.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{cfg: cfg, hc: &http.Client{Timeout: 10 * time.Second}}
}

type stsResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

// audience membangun identifier lengkap workforce pool + provider,
// sesuai yang tercatat di Provider Details (Bagian 2.10 dokumentasi):
//
//	//iam.googleapis.com/locations/global/workforcePools/{pool}/providers/{provider}
func (c *Client) audience() string {
	return fmt.Sprintf(
		"//iam.googleapis.com/locations/global/workforcePools/%s/providers/%s",
		c.cfg.WorkforcePoolID, c.cfg.WorkforceProviderID,
	)
}

// ExchangeToken menukar ID Token Microsoft (subject_token) menjadi
// Google Access Token, memakai grant type token-exchange (RFC 8693).
// Access token hasil tukar inilah yang dipakai untuk memanggil Gemini
// Enterprise API atas nama user yang login.
func (c *Client) ExchangeToken(microsoftIDToken string) (accessToken string, expiresIn int, err error) {
	aud := c.audience()
	fmt.Printf("[DEBUG] GCP STS Token Exchange:\n")
	fmt.Printf("  - Audience: %s\n", aud)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("audience", aud)
	form.Set("subject_token", microsoftIDToken)
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:jwt")
	form.Set("requested_token_type", "urn:ietf:params:oauth:token-type:access_token")
	// Request multiple scopes: cloud-platform + userinfo.email
	form.Set("scope", "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email")

	req, err := http.NewRequest(http.MethodPost, stsEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var sr stsResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", 0, err
	}
	if sr.Error != "" {
		return "", 0, fmt.Errorf("google sts error: %s - %s", sr.Error, sr.ErrorDesc)
	}
	if sr.AccessToken == "" {
		return "", 0, fmt.Errorf("google sts tidak mengembalikan access_token")
	}

	// DEBUG: Inspect token info to verify subject mapping
	tokenInfoURL := "https://oauth2.googleapis.com/tokeninfo?access_token=" + sr.AccessToken
	tokenInfoResp, err := c.hc.Get(tokenInfoURL)
	if err == nil {
		defer tokenInfoResp.Body.Close()
		var tokenInfo map[string]interface{}
		if json.NewDecoder(tokenInfoResp.Body).Decode(&tokenInfo) == nil {
			fmt.Printf("[DEBUG] Google Access Token Info:\n")
			for k, v := range tokenInfo {
				fmt.Printf("  - %s: %v\n", k, v)
			}
		}
	}

	return sr.AccessToken, sr.ExpiresIn, nil
}
