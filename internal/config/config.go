// Package config memuat semua nilai konfigurasi dari environment variable.
// Sengaja dibuat sederhana (tanpa library tambahan) supaya mudah dipahami.
package config

import (
	"log"
	"os"
)

type Config struct {
	Port string

	// --- Microsoft Entra ID (App Registration "gemini-enterprise-sso-test") ---
	EntraTenantID     string
	EntraClientID     string
	EntraClientSecret string
	EntraRedirectURI  string // contoh: http://localhost:8080/auth/callback

	// --- Microsoft Entra ID (App Registration for Outlook Connector) ---
	ConnectorClientID     string
	ConnectorClientSecret string
	ConnectorRedirectURI  string // contoh: http://localhost:8080/auth/connector/callback

	// --- GCP Workforce Identity Federation ---
	WorkforcePoolID     string // contoh: cngpt-entra-pool
	WorkforceProviderID string // contoh: entra-oidc-provider

	// --- Gemini Enterprise ---
	GCPProjectID   string // project number, mis. "945912627556"
	GeminiAppID    string // App ID Gemini Enterprise, mis. "gemini-enterprise-1783673478762"
	GeminiLocation string // "global", "us", atau "eu"

	// --- Outlook Connector ---
	OutlookConnectorID string // Connector ID dari GCP Console, mis. "outlook-federated-connector_1783678287149"

	// --- Frontend ---
	FrontendURL string // ke mana browser diarahkan setelah login sukses

	// --- Session ---
	CookieSecure bool // set true di production (HTTPS)
}

// Load membaca environment variable dan memberi nilai default yang aman untuk development lokal.
func Load() *Config {
	cfg := &Config{
		Port: getEnv("PORT", "8080"),

		EntraTenantID:     mustGetEnv("ENTRA_TENANT_ID"),
		EntraClientID:     mustGetEnv("ENTRA_CLIENT_ID"),
		EntraClientSecret: mustGetEnv("ENTRA_CLIENT_SECRET"),
		EntraRedirectURI:  getEnv("ENTRA_REDIRECT_URI", "http://localhost:8080/auth/callback"),

		ConnectorClientID:     mustGetEnv("CONNECTOR_CLIENT_ID"),
		ConnectorClientSecret: mustGetEnv("CONNECTOR_CLIENT_SECRET"),
		ConnectorRedirectURI:  getEnv("CONNECTOR_REDIRECT_URI", "http://localhost:8080/auth/connector/callback"),

		WorkforcePoolID:     mustGetEnv("GCP_WORKFORCE_POOL_ID"),
		WorkforceProviderID: mustGetEnv("GCP_WORKFORCE_PROVIDER_ID"),

		GCPProjectID:   mustGetEnv("GCP_PROJECT_ID"),
		GeminiAppID:    mustGetEnv("GEMINI_APP_ID"),
		GeminiLocation: getEnv("GEMINI_LOCATION", "global"),

		OutlookConnectorID: getEnv("OUTLOOK_CONNECTOR_ID", ""),

		FrontendURL: getEnv("FRONTEND_URL", "http://localhost:5173"),

		CookieSecure: getEnv("COOKIE_SECURE", "false") == "true",
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("environment variable %s wajib diisi (cek file .env kamu)", key)
	}
	return v
}
