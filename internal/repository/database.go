package repository

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"log"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func InitDB(cfg *config.Config) *gorm.DB {
	// FIX: Sesuaikan dengan struct di config.go lu yaitu DBURL
	dsn := cfg.DBURL

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatalf("❌ Gagal koneksi ke database: %v", err)
	}

	log.Println("✅ Berhasil koneksi ke database Supabase")

	if err := EnsureUserRoleColumn(db); err != nil {
		log.Printf("⚠️ Gagal nambah kolom users.role: %v", err)
	}

	err = db.AutoMigrate(
		&models.Merchant{},
		&models.PaymentGateway{},
		&models.GatewayEndpoint{},
		&models.DisabledPaymentMethod{},
		&models.Transaction{},
		&models.User{},
	)

	if err != nil {
		log.Printf("⚠️ Gagal migrasi otomatis: %v", err)
	} else {
		log.Println("✅ Tabel database siap digunakan")
	}

	// FIX: AUTO SEEDER UNTUK ADMIN PERTAMA KALI MENGGUNAKAN BCRYPT
	var adminCount int64
	db.Model(&models.User{}).Count(&adminCount)
	if adminCount == 0 {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("dupay123"), bcrypt.DefaultCost)
		db.Create(&models.User{
			Username: "admin",
			Password: string(hashedPassword),
			Role:     models.RoleAdmin,
		})
		log.Println("🔑 Default Admin created! (Username: admin, Password: dupay123)")
	}

	return db
}
