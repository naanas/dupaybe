package main

import (
	"dupay/internal/config"
	"dupay/internal/delivery/http"
	"dupay/internal/repository"
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Load Configuration
	cfg := config.LoadConfig()

	// 2. Connect DB pakai fungsi InitDB di folder repository
	db := repository.InitDB(cfg)

	// 3. Setup Gin Router
	r := gin.Default()

	// 4. KONFIGURASI CORS (Biar Next.js nggak kena Failed to Fetch)
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Authorization", "X-API-KEY", "X-Signature", "X-Timestamp"}
	corsConfig.ExposeHeaders = []string{"Content-Length"}
	corsConfig.AllowCredentials = true
	r.Use(cors.New(corsConfig))

	// 5. Setup Handlers
	adminHandler := http.NewAdminHandler(db, cfg)

	// 6. Setup Routes
	v1 := r.Group("/v1")
	{
		// Auth
		v1.POST("/admin/login", adminHandler.Login)

		// CMS Routes
		cms := v1.Group("/cms")
		{
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)

			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)

			cms.PUT("/merchants/:id", adminHandler.UpdateMerchant)    // TAMBAH INI
			cms.DELETE("/merchants/:id", adminHandler.DeleteMerchant) // TAMBAH INI
		}
	}

	// 7. Jalankan Server
	log.Println("🚀 Dupay Backend berjalan di port :8080")
	r.Run(":8080")
}
