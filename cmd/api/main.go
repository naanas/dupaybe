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

	// 1. Inisialisasi DB (GORM)
	db := repository.InitDB(cfg)

	// 2. Setup Gin
	gin.SetMode(cfg.GinMode)
	router := gin.Default()

	// 3. Inject DB ke Service, lalu Service ke Handler
	chargeSvc := service.NewChargeService(db)
	chargeHandler := deliveryHttp.NewChargeHandler(chargeSvc)

	// 4. Setup Routes
	v1 := router.Group("/v1")
	{
		// Endpoint khusus Klien (Diproteksi HMAC)
		protected := v1.Group("")
		protected.Use(middleware.APISecurityMiddleware())
		protected.POST("/charges", chargeHandler.CreateCharge)
		protected.GET("/charges/:id", chargeHandler.GetChargeStatus)

		// Endpoint Webhook PG (Tanpa HMAC Klien)
		webhooks := v1.Group("/webhooks")
		webhooks.POST("/midtrans", chargeHandler.MidtransWebhook)
	}

	// 5. Jalankan Server
	log.Printf("🚀 Dupay API is running on port %s", cfg.Port)
	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
