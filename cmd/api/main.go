package main

import (
	"dupay/internal/config"
	"dupay/internal/delivery/http"
	"dupay/internal/models"
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// 1. Load Configuration
	cfg := config.LoadConfig()

	// 2. Connect to Database
	db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Gagal connect ke database: %v", err)
	}

	// 3. Auto Migrate (Termasuk kolom baru di Merchant)
	db.AutoMigrate(
		&models.Merchant{},
		&models.PaymentGateway{},
		&models.Transaction{},
		&models.Admin{},
	)

	// 4. Seed Default Admin (Jika belum ada)
	seedAdmin(db)

	// 5. Setup Gin Router
	r := gin.Default()

	// --- 6. KONFIGURASI CORS (SANGAT PENTING UNTUK NEXT.JS) ---
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Authorization", "X-API-KEY", "X-Signature", "X-Timestamp"}
	corsConfig.ExposeHeaders = []string{"Content-Length"}
	corsConfig.AllowCredentials = true
	r.Use(cors.New(corsConfig))
	// ----------------------------------------------------------

	// 7. Setup Handlers
	adminHandler := http.NewAdminHandler(db, cfg)

	// 8. Setup Routes
	v1 := r.Group("/v1")
	{
		// Auth
		v1.POST("/admin/login", adminHandler.Login)

		// CMS Routes (Idealnya ini dibungkus middleware JWT, tapi untuk sekarang kita buka dulu)
		cms := v1.Group("/cms")
		{
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)

			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)
		}
	}

	// 9. Jalankan Server
	log.Println("🚀 Dupay Backend berjalan di port :8080")
	r.Run(":8080")
}

// Fungsi helper untuk membuat admin default
func seedAdmin(db *gorm.DB) {
	var count int64
	db.Model(&models.Admin{}).Count(&count)
	if count == 0 {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("dupay123"), bcrypt.DefaultCost)
		db.Create(&models.Admin{
			Username: "admin",
			Password: string(hashedPassword),
		})
		log.Println("🔑 Default Admin created! (Username: admin, Password: dupay123)")
	}
}
