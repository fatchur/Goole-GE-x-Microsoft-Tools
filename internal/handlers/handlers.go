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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"cngpt-bff-sso/internal/config"
	"cngpt-bff-sso/internal/entra"
	"cngpt-bff-sso/internal/gcpsts"
	"cngpt-bff-sso/internal/gemini"
	"cngpt-bff-sso/internal/session"
)

const (
	sessionCookieName = "cngpt_session"
	stateCookieName   = "cngpt_oauth_state"
)

type Handler struct {
	cfg          *config.Config
	entraClient  *entra.Client
	stsClient    *gcpsts.Client
	geminiClient *gemini.Client
	sessions     *session.Store
}

func New(cfg *config.Config) *Handler {
	return &Handler{
		cfg:          cfg,
		entraClient:  entra.NewClient(cfg),
		stsClient:    gcpsts.NewClient(cfg),
		geminiClient: gemini.NewClient(cfg),
		sessions:     session.NewStore(),
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

	// d) Simpan semuanya di session SERVER. Browser tidak pernah melihat idToken/googleToken.
	sessionID, err := randomString(24)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("gagal membuat session id")
	}
	h.sessions.Set(sessionID, &session.UserSession{
		Name:              claims.Name,
		Email:             claims.Email,
		GoogleAccessToken: googleToken,
		GoogleTokenExpiry: time.Now().Add(time.Duration(expiresIn) * time.Second),
		CreatedAt:         time.Now(),
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

// --- 4. POST /api/chat -----------------------------------------------

type chatRequest struct {
	Message string `json:"message"`
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

	var body chatRequest
	if err := c.BodyParser(&body); err != nil || body.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "field 'message' wajib diisi"})
	}

	raw, err := h.geminiClient.Ask(sess.GoogleAccessToken, body.Message)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	// Check if response indicates connector needs authorization
	authStatus, err := gemini.DetectConnectorAuthStatus(raw)
	if err == nil && authStatus.NeedsAuthorization {
		// Return enhanced response with authorization status
		return c.JSON(chatResponse{
			Raw: raw,
			ConnectorAuthCheck: &struct {
				NeedsAuthorization bool     `json:"needsAuthorization"`
				DetectedKeywords   []string `json:"detectedKeywords,omitempty"`
			}{
				NeedsAuthorization: true,
				DetectedKeywords:   authStatus.DetectedKeywords,
			},
		})
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
		MaxAge:   -1,               // Negative MaxAge deletes cookie
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
		"tokenInfo": tokenInfo,
		"tokenPreview": sess.GoogleAccessToken[:50] + "...",
		"note": "Token info may be empty for Workforce Identity Federation tokens",
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
