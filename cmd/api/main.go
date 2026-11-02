package main

import (
	"dupay/internal/config"
	"dupay/internal/delivery/http"
	"dupay/internal/middleware" // TAMBAHAN
	"dupay/internal/models"
	"dupay/internal/repository"
	"dupay/internal/service" // TAMBAHAN
	"log"
	"os"
	"strings"
	"time"

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
	if err := repository.EnsureUserRoleColumn(db); err != nil {
		log.Printf("Gagal nambah kolom users.role: %v", err)
	}
	db.AutoMigrate(
		&models.Merchant{},
		&models.PaymentGateway{},
		&models.GatewayEndpoint{},
		&models.DisabledPaymentMethod{},
		&models.Transaction{},
		&models.User{},
	)

	// 4. Seed Default Admin
	seedAdmin(db)

	// 5. Setup Gin Router
	r := gin.Default()

	// 6. KONFIGURASI CORS
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = getAllowedOrigins()
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Authorization", "X-API-KEY", "X-Signature", "X-Timestamp", "X-Idempotency-Key"}
	corsConfig.ExposeHeaders = []string{"Content-Length"}
	corsConfig.AllowCredentials = true
	r.Use(cors.New(corsConfig))

	r.Use(middleware.APILogger())

	// Health endpoints (public) untuk uptime checks dari LB/monitoring.
	r.GET("/health", func(c *gin.Context) {
		dbStatus := "ok"
		if err := db.Exec("SELECT 1").Error; err != nil {
			dbStatus = "degraded"
		}
		c.JSON(200, gin.H{
			"status":    "ok",
			"service":   "dupaybe",
			"db_status": dbStatus,
			"time":      time.Now().UTC().Format(time.RFC3339),
		})
	})
	// ==========================================
	// 7. SETUP SERVICES & HANDLERS
	// ==========================================

	// Admin (CMS)
	adminHandler := http.NewAdminHandler(db, cfg)

	// Client (Charge & Webhook) - INI YANG KETINGGALAN TADI!
	chargeService := service.NewChargeService(db, cfg)
	chargeHandler := http.NewChargeHandler(chargeService)
	merchantPortal := http.NewMerchantPortalHandler(db, chargeService)

	// ==========================================
	// 8. SETUP ROUTES
	// ==========================================
	v1 := r.Group("/v1")
	{
		v1.GET("/health", func(c *gin.Context) {
			dbStatus := "ok"
			if err := db.Exec("SELECT 1").Error; err != nil {
				dbStatus = "degraded"
			}
			c.JSON(200, gin.H{
				"status":    "ok",
				"service":   "dupaybe",
				"db_status": dbStatus,
				"time":      time.Now().UTC().Format(time.RFC3339),
			})
		})

		// --- ROUTES CMS (BACKOFFICE) ---
		// Login CMS buat admin & merchant (role ada di token). /admin/login = path lama.
		v1.POST("/auth/login", adminHandler.Login)
		v1.POST("/admin/login", adminHandler.Login)
		v1.POST("/auth/register", adminHandler.Register)
		jwtAuth := middleware.JWTAuthMiddleware(cfg.JWTSecret)
		v1.PUT("/auth/password", jwtAuth, http.ChangePassword(db))

		// Khusus admin: setting gateway, merchant, & semua transaksi.
		cms := v1.Group("/cms", jwtAuth, middleware.RequireRole(models.RoleAdmin))
		{
			// Merchants
			cms.POST("/merchants", adminHandler.CreateMerchant)
			cms.GET("/merchants", adminHandler.GetMerchants)
			cms.PUT("/merchants/:id", adminHandler.UpdateMerchant)
			cms.DELETE("/merchants/:id", adminHandler.DeleteMerchant)
			cms.PUT("/merchants/:id/login", adminHandler.SetMerchantLogin)
			cms.DELETE("/merchants/:id/login", adminHandler.DeleteMerchantLogin)
			cms.PUT("/merchants/:id/active", adminHandler.SetMerchantActive)

			// Gateways
			cms.POST("/gateways", adminHandler.CreateGateway)
			cms.GET("/gateways", adminHandler.GetGateways)
			cms.PUT("/gateways/:id", adminHandler.UpdateGateway)
			cms.DELETE("/gateways/:id", adminHandler.DeleteGateway)
			cms.POST("/gateways/fetch-channels", chargeHandler.FetchGatewayChannels)
			cms.POST("/gateways/test-endpoint", chargeHandler.TestGatewayEndpoint)
			cms.GET("/payment-methods/disabled", adminHandler.GetDisabledPaymentMethods)
			cms.PUT("/payment-methods/:code", adminHandler.SetPaymentMethodActive)

			// Transactions monitoring
			cms.GET("/transactions", adminHandler.GetTransactions)
			cms.POST("/transactions/:id/sync", chargeHandler.SyncTransactionCMS)
		}

		// Portal merchant: cuma data merchant yang login, tanpa info payment gateway.
		merchantAPI := v1.Group("/merchant", jwtAuth, middleware.RequireRole(models.RoleMerchant), middleware.LoadMerchant(db))
		{
			merchantAPI.GET("/me", merchantPortal.GetProfile)
			merchantAPI.PUT("/me", merchantPortal.UpdateProfile)
			merchantAPI.POST("/me/rotate-secret", merchantPortal.RotateSecret)
			merchantAPI.GET("/summary", merchantPortal.GetSummary)
			merchantAPI.GET("/transactions", merchantPortal.GetTransactions)
			merchantAPI.POST("/transactions/:id/sync", merchantPortal.SyncTransaction)
		}

		// --- ROUTES WEBHOOK (DARI PAYMENT GATEWAY TARGET) ---
		// Nggak dipasang middleware Auth karena ditembak oleh sistem luar (Tripay/Midtrans)
		v1.POST("/webhook/:gateway_name", chargeHandler.HandleWebhook)

		// --- ROUTES PUBLIC (TANPA AUTH) ---
		// Endpoint untuk fetch daftar channel aktif sebuah gateway.
		// Sengaja public supaya bisa di-cache CDN, dan dipanggil langsung dari browser.
		// Path dipisah dari /charge biar nggak konflik sama /charge/:id yang butuh auth.
		v1.GET("/channels", chargeHandler.GetChannels)

		// --- ROUTES CLIENT API (TRANSAKSI) ---
		// WAJIB pake APISecurityMiddleware (Verifikasi X-API-KEY & HMAC Signature)
		clientAPI := v1.Group("/charge")
		clientAPI.Use(middleware.APISecurityMiddleware(db))
		{
			clientAPI.POST("", chargeHandler.CreateCharge)              // POST /v1/charge
			clientAPI.GET("/:id", chargeHandler.GetChargeStatus)        // GET /v1/charge/:id
			clientAPI.POST("/:id/sync", chargeHandler.SyncChargeStatus) // POST /v1/charge/:id/sync
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

func getAllowedOrigins() []string {
	originsEnv := os.Getenv("CORS_ALLOWED_ORIGINS")
	if originsEnv == "" {
		return []string{
			"http://localhost:3000",
			"http://127.0.0.1:3000",
			"https://dupay-cms.vercel.app",
		}
	}

	parts := strings.Split(originsEnv, ",")
	origins := make([]string, 0, len(parts))
	for _, origin := range parts {
		trimmed := strings.TrimSpace(origin)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}

	// fallback aman kalau env tidak valid
	if len(origins) == 0 {
		return []string{"http://localhost:3000", "http://127.0.0.1:3000", "https://dupay-cms.vercel.app"}
	}

	return origins
}

// Fungsi helper untuk membuat admin default
func seedAdmin(db *gorm.DB) {
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count == 0 {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("dupay123"), bcrypt.DefaultCost)
		db.Create(&models.User{
			Username: "admin",
			Password: string(hashedPassword),
			Role:     models.RoleAdmin,
		})
		log.Println("🔑 Default Admin created! (Username: admin, Password: dupay123)")
	}
}
