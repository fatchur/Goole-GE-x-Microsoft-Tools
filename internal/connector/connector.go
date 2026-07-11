// Package connector handles Microsoft OAuth for Outlook connector authorization.
// This uses a SEPARATE App Registration than SSO login.
package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cngpt-bff-sso/internal/config"
)

type Client struct {
	cfg *config.Config
	hc  *http.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{cfg: cfg, hc: &http.Client{Timeout: 10 * time.Second}}
}

// AuthCodeURL builds the OAuth URL for connector authorization
func (c *Client) AuthCodeURL(state string) string {
	base := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", c.cfg.EntraTenantID)

	q := url.Values{}
	q.Set("client_id", c.cfg.ConnectorClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", c.cfg.ConnectorRedirectURI)
	// Scope for Microsoft Graph API (Outlook data access)
	q.Set("scope", "https://graph.microsoft.com/.default offline_access")
	q.Set("state", state)
	q.Set("response_mode", "query")
	// Force account selection
	q.Set("prompt", "select_account")

	return base + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// ExchangeCode exchanges authorization code for access token
func (c *Client) ExchangeCode(code string) (accessToken, refreshToken string, expiresIn int, err error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.cfg.EntraTenantID)

	form := url.Values{}
	form.Set("client_id", c.cfg.ConnectorClientID)
	form.Set("client_secret", c.cfg.ConnectorClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.cfg.ConnectorRedirectURI)
	form.Set("grant_type", "authorization_code")
	form.Set("scope", "https://graph.microsoft.com/.default offline_access")

	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", "", 0, err
	}
	if tr.Error != "" {
		return "", "", 0, fmt.Errorf("connector token error: %s - %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return "", "", 0, errors.New("connector did not return access_token")
	}

	return tr.AccessToken, tr.RefreshToken, tr.ExpiresIn, nil
}

// RefreshToken refreshes an expired connector token
func (c *Client) RefreshToken(refreshToken string) (accessToken string, newRefreshToken string, expiresIn int, err error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.cfg.EntraTenantID)

	form := url.Values{}
	form.Set("client_id", c.cfg.ConnectorClientID)
	form.Set("client_secret", c.cfg.ConnectorClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	form.Set("scope", "https://graph.microsoft.com/.default offline_access")

	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", "", 0, err
	}
	if tr.Error != "" {
		return "", "", 0, fmt.Errorf("refresh token error: %s - %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return "", "", 0, errors.New("refresh did not return access_token")
	}

	// Use new refresh token if provided, otherwise keep old one
	newRT := tr.RefreshToken
	if newRT == "" {
		newRT = refreshToken
	}

	return tr.AccessToken, newRT, tr.ExpiresIn, nil
}
