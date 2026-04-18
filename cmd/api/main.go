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
	cfg := config.LoadConfig()
	db := repository.InitDB(cfg)

	// Inisialisasi Service & Handlers
	chargeSvc := service.NewChargeService(db)
	chargeHandler := deliveryHttp.NewChargeHandler(chargeSvc)
	adminHandler := deliveryHttp.NewAdminHandler(db) // Handler Admin Baru

	gin.SetMode(cfg.GinMode)
	router := gin.Default()

	v1 := router.Group("/v1")
	{
		// --- JALUR MERCHANT (HMAC) ---
		merchant := v1.Group("")
		merchant.Use(middleware.APISecurityMiddleware())
		{
			merchant.POST("/charges", chargeHandler.CreateCharge)
			merchant.GET("/charges/:id", chargeHandler.GetChargeStatus)
		}

		// --- JALUR WEBHOOK (TIDAK ADA AUTH) ---
		v1.POST("/webhooks/midtrans", chargeHandler.MidtransWebhook)

		// --- JALUR CMS ADMIN (JWT) ---
		v1.POST("/admin/login", adminHandler.Login) // Login buat dapet token

		cms := v1.Group("/cms")
		cms.Use(middleware.JWTAuthMiddleware()) // Proteksi JWT
		{
			cms.POST("/gateways", adminHandler.CreateGateway) // Nambah PG dari UI
			cms.GET("/gateways", adminHandler.GetGateways)    // List PG di UI
		}
	}

	log.Printf("🚀 Dupay Engine & CMS API running on port %s", cfg.Port)
	router.Run(":" + cfg.Port)
}
