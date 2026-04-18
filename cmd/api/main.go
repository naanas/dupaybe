package main

import (
	"dupay/internal/config"
	deliveryHttp "dupay/internal/delivery/http"
	"dupay/internal/middleware"
	"dupay/internal/repository"
	"dupay/internal/service"
	"log"
	"time"

	"github.com/gin-contrib/cors" // FIX: Tambahkan library CORS
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.LoadConfig()
	db := repository.InitDB(cfg)

	chargeSvc := service.NewChargeService(db, cfg)
	chargeHandler := deliveryHttp.NewChargeHandler(chargeSvc)
	adminHandler := deliveryHttp.NewAdminHandler(db, cfg)

	gin.SetMode(cfg.GinMode)
	router := gin.Default()

	// FIX: MIDDLEWARE CORS DITAMBAHKAN
	// Ini memungkinkan aplikasi front-end CMS berbasis React/Vue untuk mengakses API kamu tanpa error.
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"}, // Di production, ganti "*" dengan "https://cms.dupay.com"
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-KEY", "X-Signature", "X-Timestamp", "X-Idempotency-Key", "X-Callback-Token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	v1 := router.Group("/v1")
	{
		merchant := v1.Group("")
		merchant.Use(middleware.APISecurityMiddleware(db))
		{
			merchant.POST("/charges", chargeHandler.CreateCharge)
			merchant.GET("/charges/:id", chargeHandler.GetChargeStatus)
		}

		v1.POST("/webhooks/:gateway_name", chargeHandler.HandleWebhook)

		v1.POST("/admin/login", adminHandler.Login)

		cms := v1.Group("/cms")
		cms.Use(middleware.JWTAuthMiddleware())
		{
			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)

			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)
		}
	}

	log.Printf("🚀 Dupay Engine & CMS API running on port %s", cfg.Port)
	router.Run(":" + cfg.Port)
}
