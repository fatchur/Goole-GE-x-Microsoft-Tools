// Package entra menangani sisi Microsoft: membangun URL login, menukar
// authorization code menjadi token, dan membaca klaim dasar dari ID Token.
//
// Semua request di sini dilakukan server-to-server (backend ke Microsoft),
// TIDAK PERNAH lewat browser. Client secret hanya dipakai di sini.
package entra

import (
	"encoding/base64"
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

// AuthCodeURL membangun URL redirect ke halaman login Microsoft.
// `state` dipakai untuk mencegah CSRF — nilainya dibangkitkan random oleh
// handler /auth/login, disimpan sebentar di cookie, lalu dicocokkan lagi
// saat /auth/callback dipanggil.
func (c *Client) AuthCodeURL(state string) string {
	base := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", c.cfg.EntraTenantID)

	q := url.Values{}
	q.Set("client_id", c.cfg.EntraClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", c.cfg.EntraRedirectURI)
	// openid+profile+email supaya dapat ID Token berisi nama & email.
	q.Set("scope", "openid profile email User.Read")
	q.Set("state", state)
	q.Set("response_mode", "query")
	// IMPORTANT: Force Microsoft to show account selection screen
	// This prevents auto-login when user's Microsoft session is still active
	// Options: "select_account" (show account picker) or "login" (force re-login)
	q.Set("prompt", "select_account")

	return base + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// ExchangeCode menukar authorization code (dari redirect Microsoft) menjadi
// ID Token, memakai client secret. Ini WAJIB dilakukan di server, tidak
// boleh di frontend, karena client secret ada di sini.
func (c *Client) ExchangeCode(code string) (idToken string, err error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.cfg.EntraTenantID)

	form := url.Values{}
	form.Set("client_id", c.cfg.EntraClientID)
	form.Set("client_secret", c.cfg.EntraClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.cfg.EntraRedirectURI)
	form.Set("grant_type", "authorization_code")
	form.Set("scope", "openid profile email User.Read")

	req, err := http.NewRequest(http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	if tr.Error != "" {
		return "", fmt.Errorf("entra token error: %s - %s", tr.Error, tr.ErrorDesc)
	}
	if tr.IDToken == "" {
		return "", errors.New("entra tidak mengembalikan id_token")
	}
	return tr.IDToken, nil
}

// IDTokenClaims adalah subset klaim yang kita butuhkan dari ID Token.
type IDTokenClaims struct {
	Name  string `json:"name"`
	Email string `json:"preferred_username"` // untuk akun kerja Entra ID, ini biasanya berisi email
	OID   string `json:"oid"`
}

// DecodeIDTokenUnsafe membaca payload JWT TANPA memverifikasi signature.
//
// !!! PERINGATAN UNTUK PRODUCTION !!!
// Versi ini sengaja disederhanakan untuk latihan/practice. Sebelum dipakai
// di lingkungan nyata (apalagi untuk data karyawan bank), signature JWT
// ini WAJIB diverifikasi memakai public key (JWKS) dari:
//
//	https://login.microsoftonline.com/{tenant}/discovery/v2.0/keys
//
// Gunakan library seperti github.com/coreos/go-oidc atau
// github.com/AzureAD/microsoft-authentication-library-for-go (MSAL) yang
// sudah menangani verifikasi signature, expiry, issuer, dan audience
// secara benar. Tanpa verifikasi ini, backend rentan menerima token palsu.
func DecodeIDTokenUnsafe(idToken string) (*IDTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("format ID token tidak valid")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims IDTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}
