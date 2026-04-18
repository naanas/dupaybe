package main

import (
	"dupay/internal/config"
	deliveryHttp "dupay/internal/delivery/http"
	"dupay/internal/middleware"
	"dupay/internal/repository"
	"dupay/internal/service"
	"log"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load Konfigurasi dari .env
	cfg := config.LoadConfig()

	// 2. Inisialisasi Database (Supabase/Postgres)
	db := repository.InitDB(cfg)

	// 3. Inisialisasi Service & Handlers dengan dependensi lengkap
	// ChargeService sekarang membutuhkan cfg untuk keperluan dekripsi Server Key PG
	chargeSvc := service.NewChargeService(db, cfg)
	chargeHandler := deliveryHttp.NewChargeHandler(chargeSvc)

	// AdminHandler membutuhkan cfg untuk keperluan enkripsi Server Key PG saat pendaftaran
	adminHandler := deliveryHttp.NewAdminHandler(db, cfg)

	// 4. Setup Gin Router
	gin.SetMode(cfg.GinMode)
	router := gin.Default()

	v1 := router.Group("/v1")
	{
		// --- JALUR MERCHANT / CLIENT API (HMAC) ---
		// Route ini diproteksi oleh APISecurityMiddleware yang mengecek API Key & Signature di DB
		merchant := v1.Group("")
		merchant.Use(middleware.APISecurityMiddleware(db))
		{
			merchant.POST("/charges", chargeHandler.CreateCharge)
			merchant.GET("/charges/:id", chargeHandler.GetChargeStatus)
		}

		// --- JALUR WEBHOOK UNIVERSAL (TANPA AUTH) ---
		// Mendukung semua PG secara dinamis (misal: /v1/webhooks/midtrans atau /v1/webhooks/xendit)
		v1.POST("/webhooks/:gateway_name", chargeHandler.HandleWebhook)

		// --- JALUR CMS ADMIN (JWT) ---
		// Endpoint Login untuk mendapatkan JWT Token
		v1.POST("/admin/login", adminHandler.Login)

		cms := v1.Group("/cms")
		cms.Use(middleware.JWTAuthMiddleware()) // Proteksi JWT untuk semua menu Admin
		{
			// Menu Manajemen Payment Gateway (Dinamis/Parameterized)
			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)

			// Menu Manajemen Merchant / Client (Dinamis)
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)
		}
	}

	// 5. Jalankan Server
	log.Printf("🚀 Dupay Engine & CMS API running on port %s", cfg.Port)
	router.Run(":" + cfg.Port)
}
