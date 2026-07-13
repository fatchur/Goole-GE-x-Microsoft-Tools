// cngpt-bff-sso — contoh backend Fiber sesederhana mungkin untuk praktik
// pola BFF (Backend For Frontend) SSO: Microsoft Entra ID -> Google
// Workforce Identity Federation -> Gemini Enterprise.
//
// Lihat README.md untuk cara menjalankan & penjelasan alurnya.
package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"cngpt-bff-sso/internal/config"
	"cngpt-bff-sso/internal/handlers"
)

func main() {
	// Baca file .env kalau ada (development lokal). Kalau tidak ada
	// (mis. di production, variabel di-set lewat environment container),
	// ini tidak dianggap error fatal.
	if err := godotenv.Load(); err != nil {
		log.Println("info: file .env tidak ditemukan, memakai environment variable yang sudah di-set")
	}

	cfg := config.Load()
	h := handlers.New(cfg)

	app := fiber.New(fiber.Config{
		AppName: "cngpt-bff-sso",
	})

	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		// Frontend & backend dianggap beda origin saat development lokal
		// (mis. Vite di :5173, Fiber di :8080), jadi CORS + kredensial
		// perlu diizinkan eksplisit.
		AllowOrigins:     cfg.FrontendURL,
		AllowCredentials: true,
		AllowHeaders:     "Origin, Content-Type, Accept",
	}))

	// --- Rute autentikasi ---
	app.Get("/auth/login", h.Login)
	app.Get("/auth/callback", h.Callback)
	app.Post("/auth/logout", h.Logout)

	// --- Connector authorization (OAuth terpisah untuk Outlook) ---
	app.Get("/auth/connector/authorize", h.ConnectorLogin)
	app.Get("/auth/connector/callback", h.ConnectorCallback)
	app.Get("/api/connector/status", h.ConnectorStatus)

	// --- Rute API (butuh login) ---
	app.Get("/api/me", h.Me)
	app.Post("/api/chat", h.Chat)
	app.Get("/api/datastores", h.ListDataStores)
	app.Get("/api/datastores/:id", h.GetDataStore)
	app.Get("/api/engine", h.GetEngine)

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	// --- DEBUG: Clear all sessions (development only) ---
	app.Get("/auth/debug/clear-all-sessions", h.ClearAllSessions)
	app.Get("/auth/debug/sessions", h.DebugListSessions)
	app.Get("/api/debug/token-info", h.DebugTokenInfo)

	log.Printf("cngpt-bff-sso jalan di http://localhost:%s", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
