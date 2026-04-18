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

	// Inject Dependency Config dan DB
	chargeSvc := service.NewChargeService(db, cfg)
	chargeHandler := deliveryHttp.NewChargeHandler(chargeSvc)
	adminHandler := deliveryHttp.NewAdminHandler(db, cfg)

	gin.SetMode(cfg.GinMode)
	router := gin.Default()

	v1 := router.Group("/v1")
	{
		// --- JALUR MERCHANT (HMAC) ---
		merchant := v1.Group("")
		// Masukkan database ke middleware untuk verifikasi ke tabel Merchant
		merchant.Use(middleware.APISecurityMiddleware(db))
		{
			merchant.POST("/charges", chargeHandler.CreateCharge)
			merchant.GET("/charges/:id", chargeHandler.GetChargeStatus)
		}

		// --- JALUR WEBHOOK (TIDAK ADA AUTH) ---
		v1.POST("/webhooks/midtrans", chargeHandler.MidtransWebhook)

		// --- JALUR CMS ADMIN (JWT) ---
		v1.POST("/admin/login", adminHandler.Login)

		cms := v1.Group("/cms")
		cms.Use(middleware.JWTAuthMiddleware())
		{
			// Endpoint Gateway
			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)

			// Endpoint Merchant
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)
		}
	}

	log.Printf("🚀 Dupay Engine & CMS API running on port %s", cfg.Port)
	router.Run(":" + cfg.Port)
}
