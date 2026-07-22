// Package handlers berisi seluruh HTTP handler Fiber.
//
// Alur BFF (Backend For Frontend) yang diimplementasikan:
//
//  1. GET  /auth/login    -> redirect browser ke Microsoft
//  2. GET  /auth/callback -> tukar code -> ID Token -> Google Access Token,
//     lalu simpan di session server, browser hanya dapat cookie
//  3. GET  /api/me        -> info user yang login (dari session, bukan token)
//  4. POST /api/chat      -> backend yang panggil Gemini Enterprise,
//     browser tidak pernah pegang token
//  5. POST /auth/logout   -> hapus session
package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"cngpt-bff-sso/internal/config"
	"cngpt-bff-sso/internal/connector"
	"cngpt-bff-sso/internal/entra"
	"cngpt-bff-sso/internal/gcpsts"
	"cngpt-bff-sso/internal/gcs"
	"cngpt-bff-sso/internal/gemini"
	"cngpt-bff-sso/internal/session"
)

const (
	sessionCookieName       = "cngpt_session"
	stateCookieName         = "cngpt_oauth_state"
	connectorStateCookieame = "cngpt_connector_state"
)

type Handler struct {
	cfg             *config.Config
	entraClient     *entra.Client
	connectorClient *connector.Client
	stsClient       *gcpsts.Client
	geminiClient    *gemini.Client
	gcsClient       *gcs.Client
	sessions        *session.Store

	// Connector OAuth state tracking
	connectorStatesMu sync.RWMutex
	connectorStates   map[string]string // state -> sessionID
}

func New(cfg *config.Config) *Handler {
	// Initialize GCS client (with error handling)
	gcsClient, err := gcs.NewClient(cfg)
	if err != nil {
		log.Printf("[WARNING] Failed to initialize GCS client: %v", err)
		log.Printf("[WARNING] File upload will not work. Make sure GOOGLE_APPLICATION_CREDENTIALS is set.")
	}

	return &Handler{
		cfg:             cfg,
		entraClient:     entra.NewClient(cfg),
		connectorClient: connector.NewClient(cfg),
		stsClient:       gcpsts.NewClient(cfg),
		geminiClient:    gemini.NewClient(cfg),
		gcsClient:       gcsClient,
		sessions:        session.NewStore(),
		connectorStates: make(map[string]string),
	}
}

// randomString menghasilkan string acak untuk state (anti-CSRF) dan session ID.
func randomString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// extractPrincipalFromGoogleToken decodes Google Access Token (JWT) and extracts the principal (sub claim)
// Google Workforce Identity tokens have format: principal://iam.googleapis.com/projects/.../subject/ABC123
func extractPrincipalFromGoogleToken(token string) (string, error) {
	// JWT format: header.payload.signature
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("invalid JWT format")
	}

	// Decode payload (base64 URL encoding)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	// Parse JSON to extract "sub" claim
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	if claims.Sub == "" {
		return "", errors.New("sub claim not found in token")
	}

	return claims.Sub, nil
}

// --- 1. GET /auth/login -----------------------------------------------

func (h *Handler) Login(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🔐 /auth/login called - initiating OAuth flow")

	// Check if there's already an active session
	_, sessionID, hasSession := h.currentSession(c)
	if hasSession {
		fmt.Printf("[DEBUG] ⚠️  Active session detected: %s (will be ignored, proceeding with login)\n", sessionID)
	} else {
		fmt.Println("[DEBUG] No active session, proceeding with fresh login")
	}

	state, err := randomString(16)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("gagal membuat state")
	}

	// Simpan state di cookie sementara (5 menit) untuk dicocokkan nanti
	// saat callback — mencegah serangan CSRF pada alur OAuth.
	c.Cookie(&fiber.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Expires:  time.Now().Add(5 * time.Minute),
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})

	authURL := h.entraClient.AuthCodeURL(state)
	fmt.Printf("[DEBUG] Redirecting to Microsoft OAuth:\n  %s\n", authURL)
	fmt.Printf("[DEBUG] State: %s\n", state)

	return c.Redirect(authURL, fiber.StatusFound)
}

// --- 2. GET /auth/callback ---------------------------------------------

func (h *Handler) Callback(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🔄 /auth/callback called - Microsoft redirected back")

	// a) Validasi state (anti-CSRF).
	expectedState := c.Cookies(stateCookieName)
	gotState := c.Query("state")
	fmt.Printf("[DEBUG] State validation - Expected: %s, Got: %s\n", expectedState, gotState)
	if expectedState == "" || expectedState != gotState {
		return c.Status(fiber.StatusBadRequest).SendString("state tidak cocok, kemungkinan serangan CSRF atau sesi login kedaluwarsa")
	}
	c.ClearCookie(stateCookieName)

	code := c.Query("code")
	if code == "" {
		return c.Status(fiber.StatusBadRequest).SendString("parameter 'code' tidak ditemukan dari Microsoft")
	}

	// b) Tukar authorization code -> ID Token Microsoft (server-to-server).
	idToken, err := h.entraClient.ExchangeCode(code)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).SendString("gagal menukar code ke Microsoft: " + err.Error())
	}

	claims, err := entra.DecodeIDTokenUnsafe(idToken)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("gagal membaca ID token: " + err.Error())
	}

	// DEBUG: Log claims dari Microsoft
	fmt.Printf("[DEBUG] Microsoft ID Token Claims:\n")
	fmt.Printf("  - Name: %s\n", claims.Name)
	fmt.Printf("  - Email: %s\n", claims.Email)
	fmt.Printf("  - Subject (oid): %s\n", claims.OID)

	// c) Tukar ID Token Microsoft -> Google Access Token (Workforce Identity Federation).
	googleToken, expiresIn, err := h.stsClient.ExchangeToken(idToken)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).SendString("gagal tukar token ke Google STS: " + err.Error())
	}

	// DEBUG: Log Google token info
	fmt.Printf("[DEBUG] Google Access Token received (expires in %d seconds)\n", expiresIn)
	fmt.Printf("[DEBUG] Token preview: %s...\n", googleToken[:50])

	// Extract Workforce Principal from Google token (needed for connector authorization)
	principal, err := extractPrincipalFromGoogleToken(googleToken)
	if err != nil {
		fmt.Printf("[DEBUG] ⚠️  Failed to extract principal from token: %v\n", err)
		principal = "" // Continue without principal (optional feature)
	} else {
		fmt.Printf("[DEBUG] ✅ Workforce Principal extracted: %s\n", principal)
	}

	// d) Simpan semuanya di session SERVER. Browser tidak pernah melihat idToken/googleToken.
	sessionID, err := randomString(24)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("gagal membuat session id")
	}
	h.sessions.Set(sessionID, &session.UserSession{
		Name:               claims.Name,
		Email:              claims.Email,
		GoogleAccessToken:  googleToken,
		GoogleTokenExpiry:  time.Now().Add(time.Duration(expiresIn) * time.Second),
		WorkforcePrincipal: principal,
		CreatedAt:          time.Now(),
	})

	// e) Browser hanya diberi session ID lewat cookie httpOnly.
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Expires:  time.Now().Add(8 * time.Hour),
		HTTPOnly: true, // <- kunci utama: JavaScript di browser TIDAK BISA baca cookie ini
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/", // Explicit path to match logout
	})

	fmt.Printf("[DEBUG] ✅ Login successful! Created session: %s for user: %s\n", sessionID, claims.Email)
	fmt.Printf("[DEBUG] Redirecting to frontend: %s\n", h.cfg.FrontendURL)
	return c.Redirect(h.cfg.FrontendURL, fiber.StatusFound)
}

// --- Middleware: pastikan request punya session valid -------------------

func (h *Handler) currentSession(c *fiber.Ctx) (*session.UserSession, string, bool) {
	sessionID := c.Cookies(sessionCookieName)
	if sessionID == "" {
		return nil, "", false
	}
	sess, ok := h.sessions.Get(sessionID)
	return sess, sessionID, ok
}

// --- 3. GET /api/me ------------------------------------------------------

func (h *Handler) Me(c *fiber.Ctx) error {
	sessionCookie := c.Cookies(sessionCookieName)
	fmt.Printf("[DEBUG] 👤 /api/me called, session cookie: %s\n", sessionCookie)

	sess, sessionID, ok := h.currentSession(c)
	if !ok {
		fmt.Println("[DEBUG] ❌ No valid session found")
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	fmt.Printf("[DEBUG] ✅ Valid session found: %s (user: %s)\n", sessionID, sess.Email)
	return c.JSON(fiber.Map{
		"name":  sess.Name,
		"email": sess.Email,
	})
}

// --- 4. POST /api/files/upload ----------------------------------------

type fileUploadResponse struct {
	GCSURI   string `json:"gcsUri"`   // GCS URI (gs://bucket/path)
	FileName string `json:"fileName"` // Original filename
	FileSize int64  `json:"fileSize"` // File size in bytes
	MimeType string `json:"mimeType"` // MIME type
}

func (h *Handler) UploadFile(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	if time.Now().After(sess.GoogleTokenExpiry) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "sesi Google kedaluwarsa, silakan login ulang",
		})
	}

	// Check if GCS client is initialized
	if h.gcsClient == nil {
		fmt.Printf("[DEBUG] ❌ GCS client not initialized\n")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "file upload service not available (GCS not configured)",
		})
	}

	// Get file from form
	fileHeader, err := c.FormFile("file")
	if err != nil {
		fmt.Printf("[DEBUG] ❌ Failed to get file from form: %v\n", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file not found in request"})
	}

	fileName := fileHeader.Filename
	fileMimeType := fileHeader.Header.Get("Content-Type")
	fileSize := fileHeader.Size

	fmt.Printf("[DEBUG] 📤 File upload request:\n")
	fmt.Printf("  - User: %s\n", sess.Email)
	fmt.Printf("  - File name: %s\n", fileName)
	fmt.Printf("  - MIME type: %s\n", fileMimeType)
	fmt.Printf("  - Size: %d bytes (%.2f KB)\n", fileSize, float64(fileSize)/1024)

	// Validate file size (max 10MB)
	maxSize := int64(10 * 1024 * 1024)
	if fileSize > maxSize {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file too large, maximum 10MB"})
	}

	// Open file
	file, err := fileHeader.Open()
	if err != nil {
		fmt.Printf("[DEBUG] ❌ Failed to open file: %v\n", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read file"})
	}
	defer file.Close()

	// Upload file to GCS
	fmt.Printf("[DEBUG] Uploading file to GCS...\n")
	ctx := c.Context()
	gcsURI, err := h.gcsClient.UploadFile(ctx, fileName, fileMimeType, file)
	if err != nil {
		fmt.Printf("[DEBUG] ❌ Failed to upload file to GCS: %v\n", err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "failed to upload file: " + err.Error()})
	}

	fmt.Printf("[DEBUG] ✅ File uploaded successfully: %s\n", gcsURI)

	return c.JSON(fileUploadResponse{
		GCSURI:   gcsURI,
		FileName: fileName,
		FileSize: fileSize,
		MimeType: fileMimeType,
	})
}

// --- 5. POST /api/chat -----------------------------------------------

type chatRequest struct {
	Message string   `json:"message"`
	GCSURIs []string `json:"gcsUris,omitempty"` // Optional GCS URIs from upload (gs://bucket/path)
}

type chatResponse struct {
	Raw                json.RawMessage `json:"raw"`
	ConnectorAuthCheck *struct {
		NeedsAuthorization bool     `json:"needsAuthorization"`
		DetectedKeywords   []string `json:"detectedKeywords,omitempty"`
	} `json:"connectorAuthCheck,omitempty"`
}

func (h *Handler) Chat(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	if time.Now().After(sess.GoogleTokenExpiry) {
		// Sederhana: untuk latihan ini, minta user login ulang saat token habis.
		// Di production, sebaiknya token di-refresh otomatis oleh backend.
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "sesi Google kedaluwarsa, silakan login ulang",
		})
	}

	// Parse JSON request body
	var body chatRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "field 'message' wajib diisi"})
	}

	fmt.Printf("[DEBUG] 💬 Chat request:\n")
	fmt.Printf("  - User: %s\n", sess.Email)
	fmt.Printf("  - Message: %s\n", body.Message)
	fmt.Printf("  - GCS URIs: %v\n", body.GCSURIs)

	// PENTING: Gemini API SELALU dipanggil dengan Google Workforce Identity token
	// Connector token (Microsoft Graph) TIDAK digunakan untuk memanggil Gemini API
	// Connector authorization hanya memberi permission ke Gemini untuk akses Outlook data
	if sess.ConnectorAuthorized {
		fmt.Printf("[DEBUG] Connector authorized - Gemini can access Outlook data\n")
	} else {
		fmt.Printf("[DEBUG] Connector NOT authorized - Gemini cannot access Outlook data\n")
	}

	// Call Gemini API (with or without files)
	var raw json.RawMessage
	var err error

	if len(body.GCSURIs) > 0 {
		// Query with GCS file context (files already uploaded via /api/files/upload)
		fmt.Printf("[DEBUG] Querying Gemini with GCS file context\n")
		raw, err = h.geminiClient.AskWithGCSFiles(sess.GoogleAccessToken, body.GCSURIs, body.Message)
		if err != nil {
			fmt.Printf("[DEBUG] ❌ Failed to query with GCS files: %v\n", err)
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Failed to query with files: " + err.Error()})
		}
	} else {
		// Send text only (backward compatible - no changes to existing flow)
		fmt.Printf("[DEBUG] Calling Gemini API with text only (no files)\n")
		raw, err = h.geminiClient.Ask(sess.GoogleAccessToken, body.Message)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
		}
	}

	// Kirim balik apa adanya (raw JSON dari Gemini Enterprise) supaya
	// frontend bebas menampilkan sesuai kebutuhan.
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(raw)
}

// --- 5. POST /auth/logout ---------------------------------------------

func (h *Handler) Logout(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🚪 Logout requested")

	_, sessionID, ok := h.currentSession(c)
	if ok {
		fmt.Printf("[DEBUG] Deleting session: %s\n", sessionID)
		h.sessions.Delete(sessionID)
	} else {
		fmt.Println("[DEBUG] No active session found")
	}

	// Clear session cookie with explicit settings to ensure removal
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Expires:  time.Unix(0, 0), // Set to past time to delete
		MaxAge:   -1,              // Negative MaxAge deletes cookie
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/", // Important: must match original cookie path
	})

	// Also clear state cookie if it exists
	c.Cookie(&fiber.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})

	fmt.Println("[DEBUG] ✅ Logout complete, cookies cleared")
	return c.JSON(fiber.Map{"message": "logout berhasil"})
}

// --- 6. GET /api/datastores -----------------------------------------------
// List all data stores (including connectors)

func (h *Handler) ListDataStores(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	if time.Now().After(sess.GoogleTokenExpiry) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "sesi Google kedaluwarsa, silakan login ulang",
		})
	}

	raw, err := h.geminiClient.ListDataStores(sess.GoogleAccessToken)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(raw)
}

// --- 7. GET /api/datastores/:id -------------------------------------------
// Get detailed data store information (including connector state)

func (h *Handler) GetDataStore(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	if time.Now().After(sess.GoogleTokenExpiry) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "sesi Google kedaluwarsa, silakan login ulang",
		})
	}

	dataStoreID := c.Params("id")
	if dataStoreID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "data store ID required"})
	}

	raw, err := h.geminiClient.GetDataStore(sess.GoogleAccessToken, dataStoreID)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(raw)
}

// --- 8. GET /api/engine ---------------------------------------------------
// Get engine configuration (including connected data stores)

func (h *Handler) GetEngine(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	if time.Now().After(sess.GoogleTokenExpiry) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "sesi Google kedaluwarsa, silakan login ulang",
		})
	}

	raw, err := h.geminiClient.GetEngine(sess.GoogleAccessToken)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(raw)
}

// --- 9. GET /api/debug/token-info -----------------------------------------
// Get token information for debugging authentication issues

func (h *Handler) DebugTokenInfo(c *fiber.Ctx) error {
	sess, sessionID, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "belum login"})
	}

	fmt.Printf("[DEBUG] 🔍 Token info requested for session: %s\n", sessionID)

	// Try to get token info from Google
	tokenInfoURL := "https://oauth2.googleapis.com/tokeninfo?access_token=" + sess.GoogleAccessToken
	resp, err := http.Get(tokenInfoURL)

	var tokenInfo map[string]interface{}
	if err == nil && resp != nil {
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(&tokenInfo)
	}

	return c.JSON(fiber.Map{
		"session": fiber.Map{
			"sessionID":   sessionID,
			"name":        sess.Name,
			"email":       sess.Email,
			"tokenExpiry": sess.GoogleTokenExpiry,
			"createdAt":   sess.CreatedAt,
		},
		"tokenInfo":    tokenInfo,
		"tokenPreview": sess.GoogleAccessToken[:50] + "...",
		"note":         "Token info may be empty for Workforce Identity Federation tokens",
	})
}

// --- DEBUG: GET /auth/debug/clear-all-sessions ----------------------------
// HANYA UNTUK DEVELOPMENT - hapus semua session di backend

func (h *Handler) ClearAllSessions(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🧹 Clearing ALL sessions (debug endpoint)")

	// Clear all sessions from store
	h.sessions.Clear()
	fmt.Println("[DEBUG] All sessions cleared from store")

	// Clear session cookie with explicit settings
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})

	// Clear state cookie
	c.Cookie(&fiber.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})

	fmt.Println("[DEBUG] ✅ All sessions and cookies cleared")
	return c.JSON(fiber.Map{
		"message":        "semua session telah dihapus",
		"sessionCleared": true,
		"instruction":    "Please refresh the page or close and reopen your browser",
	})
}

// --- DEBUG: GET /auth/debug/sessions --------------------------------------
// HANYA UNTUK DEVELOPMENT - lihat semua session aktif

func (h *Handler) DebugListSessions(c *fiber.Ctx) error {
	count := h.sessions.Count()
	sessionIDs := h.sessions.List()

	fmt.Printf("[DEBUG] 📋 Listing sessions: %d active\n", count)
	for i, id := range sessionIDs {
		fmt.Printf("[DEBUG]   %d. %s\n", i+1, id)
	}

	return c.JSON(fiber.Map{
		"count":      count,
		"sessionIDs": sessionIDs,
	})
}

// --- CONNECTOR AUTHORIZATION FLOW ------------------------------------------

// getOutlookConnectorID retrieves the Outlook connector ID from data stores list
func (h *Handler) getOutlookConnectorID(googleAccessToken string) (string, error) {
	raw, err := h.geminiClient.ListDataStores(googleAccessToken)
	if err != nil {
		return "", fmt.Errorf("failed to list data stores: %w", err)
	}

	fmt.Printf("[DEBUG] Data stores raw response: %s\n", string(raw))

	// Parse response to find Outlook connector
	var response struct {
		DataStores []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"dataStores"`
	}

	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("failed to parse data stores response: %w", err)
	}

	fmt.Printf("[DEBUG] Found %d data stores\n", len(response.DataStores))

	// Look for outlook connector (any of: _mail, _calendar, _contact)
	for _, ds := range response.DataStores {
		fmt.Printf("[DEBUG] Data store: name=%s, displayName=%s\n", ds.Name, ds.DisplayName)

		// Connector name pattern: projects/.../dataStores/outlook-federated-connector_1783678287149_mail
		if strings.Contains(ds.Name, "outlook-federated-connector") {

			// Extract the dataStore ID from path
			// Name: "projects/945912627556/locations/global/collections/default_collection/dataStores/outlook-federated-connector_1783678287149_mail"
			// Need: "collections/outlook-federated-connector_1783678287149/dataConnector"

			parts := strings.Split(ds.Name, "/")
			fmt.Printf("[DEBUG] Name parts: %v\n", parts)

			for i, part := range parts {
				if part == "dataStores" && i+1 < len(parts) {
					fullDataStoreID := parts[i+1] // e.g. "outlook-federated-connector_1783678287149_mail"
					fmt.Printf("[DEBUG] Full dataStore ID: %s\n", fullDataStoreID)

					// Extract base connector ID by removing suffix (_mail, _calendar, _contact)
					// Pattern: outlook-federated-connector_{TIMESTAMP}_{SUFFIX}
					// We need: outlook-federated-connector_{TIMESTAMP}

					baseID := fullDataStoreID
					// Find last underscore to remove suffix
					lastUnderscore := strings.LastIndex(fullDataStoreID, "_")
					if lastUnderscore > 0 {
						// Check if it's a known suffix
						suffix := fullDataStoreID[lastUnderscore+1:]
						if suffix == "mail" || suffix == "calendar" || suffix == "contact" || suffix == "attachment" {
							baseID = fullDataStoreID[:lastUnderscore]
							fmt.Printf("[DEBUG] Removed suffix '%s', base ID: %s\n", suffix, baseID)
						}
					}

					connectorID := fmt.Sprintf("collections/%s/dataConnector", baseID)
					fmt.Printf("[DEBUG] Final connector ID: %s\n", connectorID)
					return connectorID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("outlook connector not found in data stores")
}

// --- 10. GET /auth/connector/login ----------------------------------------
// Initiate connector OAuth flow (separate from SSO login)

func (h *Handler) ConnectorLogin(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🔌 /auth/connector/login called - initiating connector OAuth flow")

	// Must already be logged in (have a session)
	sess, sessionID, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).SendString("Must be logged in first before authorizing connector")
	}

	fmt.Printf("[DEBUG] User %s (%s) requesting connector authorization\n", sess.Email, sessionID)
	fmt.Printf("[DEBUG] SessionID length: %d chars\n", len(sessionID))
	fmt.Printf("[DEBUG] SessionID hex: %x\n", []byte(sessionID))

	// Generate state for CSRF protection
	state, err := randomString(16)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to generate state")
	}

	// Save state -> sessionID mapping (so we can find the session on callback)
	h.connectorStatesMu.Lock()

	// Check if state already exists (shouldn't happen, but guard against corruption)
	if existingSessionID, exists := h.connectorStates[state]; exists {
		fmt.Printf("[DEBUG] ⚠️ WARNING: State %s already exists in map with sessionID: %s\n", state, existingSessionID)
	}

	// Make a copy of sessionID string to prevent any buffer sharing issues
	sessionIDCopy := string([]byte(sessionID))
	h.connectorStates[state] = sessionIDCopy
	h.connectorStatesMu.Unlock()

	fmt.Printf("[DEBUG] Saved connector state mapping:\n")
	fmt.Printf("  State: %s (%d chars)\n", state, len(state))
	fmt.Printf("  SessionID: %s (%d chars)\n", sessionIDCopy, len(sessionIDCopy))

	// Save state in cookie as well for validation
	c.Cookie(&fiber.Cookie{
		Name:     connectorStateCookieame,
		Value:    state,
		Expires:  time.Now().Add(10 * time.Minute),
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})

	// 🧪 EXPERIMENT 4: Use EXACT state format from Google WebApp (network capture)
	// Discovery: State format completely different from our assumptions!
	// Format based on actual network capture from working WebApp flow.
	googleCallbackURL := "https://vertexaisearch.cloud.google.com/oauth-redirect"

	// Build connector path in Google's format
	connectorPath := fmt.Sprintf("collections/%s/dataConnector", h.cfg.OutlookConnectorID)

	// Build state with EXACT structure from Google WebApp network capture
	stateData := map[string]interface{}{
		"origin":    "https://vertexaisearch.cloud.google",
		"requestId": "ucs-federated-sources-0", // Request ID from WebApp, kalau dari custom app apa ? gmana cara dapetnya ?
		"extraData": map[string]interface{}{
			"dataConnectors": []string{connectorPath},
			"sourceType":     h.cfg.OutlookConnectorID,
			"Rr":             "outlook",
			"extraData": map[string]interface{}{
				"value":           h.cfg.OutlookConnectorID,
				"kind":            "outlook",
				"rq":              "outlook",
				"dataConnector":   connectorPath,
				"actionConnector": connectorPath,
				"gq":              true,
				"authState":       "AUTHORIZED",
				"label":           "",
				"ue":              []interface{}{},
			},
		},
	}

	stateJSON, err := json.Marshal(stateData)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to build state")
	}

	// Encode as base64 URL-safe
	stateEncoded := base64.URLEncoding.EncodeToString(stateJSON)

	authURL := h.connectorClient.AuthCodeURLWithCustomRedirect(stateEncoded, googleCallbackURL)
	fmt.Printf("[DEBUG] 🧪 EXPERIMENT 4: Using EXACT state format from WebApp network capture!\n")
	fmt.Printf("  User: %s\n", sess.Email)
	fmt.Printf("  Connector Path: %s\n", connectorPath)
	fmt.Printf("  State JSON: %s\n", string(stateJSON))
	fmt.Printf("  State Base64: %s\n", stateEncoded)
	fmt.Printf("  Authorization URL: %s\n", authURL)
	fmt.Printf("  Callback will go to: %s\n", googleCallbackURL)
	fmt.Printf("  ⚠️  Note: After authorization, you won't be redirected back to this app.\n")
	fmt.Printf("  ⚠️  Close the browser tab and return to test if connector access works.\n")

	return c.Redirect(authURL, fiber.StatusFound)
}

// --- 11. GET /auth/connector/callback -------------------------------------
// Handle connector OAuth callback

func (h *Handler) ConnectorCallback(c *fiber.Ctx) error {
	fmt.Println("[DEBUG] 🔄 /auth/connector/callback called - Microsoft redirected back")

	// Validate state
	expectedState := c.Cookies(connectorStateCookieame)
	gotState := c.Query("state")
	fmt.Printf("[DEBUG] Connector state validation - Expected: %s, Got: %s\n", expectedState, gotState)

	if expectedState == "" || expectedState != gotState {
		return c.Status(fiber.StatusBadRequest).SendString("State mismatch - possible CSRF attack or expired session")
	}
	c.ClearCookie(connectorStateCookieame)

	// Find the session ID associated with this state
	h.connectorStatesMu.RLock()

	// Debug: print all keys and values in map
	fmt.Printf("[DEBUG] === Connector States Map Dump ===\n")
	for k, v := range h.connectorStates {
		fmt.Printf("  Key: %s (%d chars) -> Value: %s (%d chars)\n", k, len(k), v, len(v))
	}
	fmt.Printf("[DEBUG] === End Map Dump ===\n")

	sessionID, stateExists := h.connectorStates[gotState]
	totalStates := len(h.connectorStates)
	h.connectorStatesMu.RUnlock()

	fmt.Printf("[DEBUG] State lookup:\n")
	fmt.Printf("  Looking for state: %s (%d chars)\n", gotState, len(gotState))
	fmt.Printf("  State exists in map: %v\n", stateExists)
	fmt.Printf("  Total states in map: %d\n", totalStates)
	if stateExists {
		fmt.Printf("  Retrieved SessionID: %s (%d chars)\n", sessionID, len(sessionID))
	}

	if !stateExists {
		fmt.Printf("[DEBUG] ❌ State not found in connectorStates map. Available states: %d\n", len(h.connectorStates))
		return c.Status(fiber.StatusBadRequest).SendString("Invalid state - session expired or not found")
	}

	// Get the session BEFORE cleaning up state (so we can debug)
	sess, ok := h.sessions.Get(sessionID)
	fmt.Printf("[DEBUG] Session lookup - exists: %v\n", ok)
	if !ok {
		fmt.Printf("[DEBUG] ❌ Session not found for ID: %s\n", sessionID)
		fmt.Printf("[DEBUG] Total sessions in store: %d\n", h.sessions.Count())
		return c.Status(fiber.StatusBadRequest).SendString("Session not found - please login again")
	}

	// Clean up state after successful validation
	h.connectorStatesMu.Lock()
	delete(h.connectorStates, gotState)
	h.connectorStatesMu.Unlock()

	code := c.Query("code")
	if code == "" {
		return c.Status(fiber.StatusBadRequest).SendString("No authorization code received from Microsoft")
	}

	fmt.Printf("[DEBUG] Received authorization code from Microsoft: %s...\n", code[:50])

	// IMPORTANT: DO NOT exchange code ourselves!
	// Pass full redirect URI (with code and state) to Google Discovery Engine API
	// Google will exchange the code and store the refresh token

	// Step 1: Get connector ID from env (use configured Outlook connector ID)
	connectorID := h.cfg.OutlookConnectorID
	if connectorID == "" {
		// Fallback: try to get from data stores list
		fmt.Println("[DEBUG] Outlook connector ID not in config, fetching from data stores...")
		var err error
		connectorID, err = h.getOutlookConnectorID(sess.GoogleAccessToken)
		if err != nil {
			fmt.Printf("[DEBUG] ❌ Failed to get connector ID: %v\n", err)
			return c.Status(fiber.StatusInternalServerError).SendString("Connector ID not configured")
		}
	}
	fmt.Printf("[DEBUG] Using connector ID: %s\n", connectorID)

	// Step 2: Build full redirect URI (the full URL Microsoft redirected to)
	fullRedirectURI := fmt.Sprintf("%s?code=%s&state=%s",
		h.cfg.ConnectorRedirectURI,
		code,
		gotState,
	)

	// Step 3: Call Discovery Engine API to acquire and store refresh token
	fmt.Println("[DEBUG] Calling Discovery Engine API to acquire and store refresh token...")
	if err := h.geminiClient.AcquireAndStoreRefreshToken(sess.GoogleAccessToken, connectorID, fullRedirectURI); err != nil {
		fmt.Printf("[DEBUG] ❌ Failed to acquire and store refresh token: %v\n", err)
		return c.Status(fiber.StatusBadGateway).SendString("Failed to store connector authorization: " + err.Error())
	}

	// Mark connector as authorized in session (no need to store tokens - Google has them)
	sess.ConnectorAuthorized = true
	h.sessions.Set(sessionID, sess)

	fmt.Printf("[DEBUG] ✅ Connector authorized for user: %s\n", sess.Email)

	// Close the popup window with a simple HTML page
	html := `<!DOCTYPE html>
<html>
<head>
	<title>Connector Authorized</title>
	<style>
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
			display: flex;
			justify-content: center;
			align-items: center;
			height: 100vh;
			margin: 0;
			background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
			color: white;
		}
		.container {
			text-align: center;
			padding: 2rem;
			background: rgba(255, 255, 255, 0.1);
			border-radius: 12px;
			backdrop-filter: blur(10px);
		}
		h1 { margin: 0 0 1rem 0; font-size: 2rem; }
		p { margin: 0.5rem 0; opacity: 0.9; }
		.checkmark { font-size: 4rem; margin-bottom: 1rem; }
	</style>
</head>
<body>
	<div class="container">
		<div class="checkmark">✅</div>
		<h1>Outlook Connector Authorized!</h1>
		<p>You can now access your emails, calendar, and contacts.</p>
		<p style="margin-top: 1.5rem; font-size: 0.9rem;">This window will close automatically...</p>
	</div>
	<script>
		// Close popup after 2 seconds
		setTimeout(() => {
			window.close();
		}, 2000);
	</script>
</body>
</html>`

	c.Set(fiber.HeaderContentType, fiber.MIMETextHTML)
	return c.SendString(html)
}

// --- 12. GET /api/connector/status ----------------------------------------
// Check connector authorization status

func (h *Handler) ConnectorStatus(c *fiber.Ctx) error {
	sess, _, ok := h.currentSession(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Not logged in"})
	}

	return c.JSON(fiber.Map{
		"authorized":      sess.ConnectorAuthorized,
		"expiry":          sess.ConnectorTokenExpiry,
		"hasRefreshToken": sess.ConnectorRefreshToken != "",
	})
}
