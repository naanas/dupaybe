package main

import (
	"dupay/internal/config"
	"dupay/internal/delivery/http"
	"dupay/internal/middleware" // TAMBAHAN
	"dupay/internal/models"
	"dupay/internal/service" // TAMBAHAN
	"log"
	"os"

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

	// 3. Auto Migrate
	db.AutoMigrate(
		&models.Merchant{},
		&models.PaymentGateway{},
		&models.Transaction{},
		&models.Admin{},
	)

	// 4. Seed Default Admin
	seedAdmin(db)

	// 5. Setup Gin Router
	r := gin.Default()

	// 6. KONFIGURASI CORS
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Authorization", "X-API-KEY", "X-Signature", "X-Timestamp", "X-Idempotency-Key"}
	corsConfig.ExposeHeaders = []string{"Content-Length"}
	corsConfig.AllowCredentials = true
	r.Use(cors.New(corsConfig))

	r.Use(middleware.APILogger())
	// ==========================================
	// 7. SETUP SERVICES & HANDLERS
	// ==========================================

	// Admin (CMS)
	adminHandler := http.NewAdminHandler(db, cfg)

	// Client (Charge & Webhook) - INI YANG KETINGGALAN TADI!
	chargeService := service.NewChargeService(db, cfg)
	chargeHandler := http.NewChargeHandler(chargeService)

	// ==========================================
	// 8. SETUP ROUTES
	// ==========================================
	v1 := r.Group("/v1")
	{
		// --- ROUTES CMS (BACKOFFICE) ---
		v1.POST("/admin/login", adminHandler.Login)
		cms := v1.Group("/cms")
		{
			// Merchants
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)
			cms.PUT("/merchants/:id", adminHandler.UpdateMerchant)
			cms.DELETE("/merchants/:id", adminHandler.DeleteMerchant)

			// Gateways
			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)
			cms.PUT("/gateways/:id", adminHandler.UpdateGateway)
			cms.DELETE("/gateways/:id", adminHandler.DeleteGateway)

			// Transactions monitoring
			cms.GET("/transactions", adminHandler.GetTransactions)
		}

		// --- ROUTES WEBHOOK (DARI PAYMENT GATEWAY TARGET) ---
		// Nggak dipasang middleware Auth karena ditembak oleh sistem luar (Tripay/Midtrans)
		v1.POST("/webhook/:gateway_name", chargeHandler.HandleWebhook)

		// --- ROUTES CLIENT API (TRANSAKSI) ---
		// WAJIB pake APISecurityMiddleware (Verifikasi X-API-KEY & HMAC Signature)
		clientAPI := v1.Group("/charge")
		clientAPI.Use(middleware.APISecurityMiddleware(db))
		{
			clientAPI.POST("", chargeHandler.CreateCharge)       // POST /v1/charge
			clientAPI.GET("/:id", chargeHandler.GetChargeStatus) // GET /v1/charge/:id
		}
	}

	// 9. Jalankan Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("🚀 Dupay Backend berjalan di port :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("gagal menjalankan server: %v", err)
	}
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
