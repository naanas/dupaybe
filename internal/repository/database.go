package repository

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func InitDB(cfg *config.Config) *gorm.DB {
	dsn := cfg.DatabaseURL

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatalf("❌ Gagal koneksi ke database: %v", err)
	}

	log.Println("✅ Berhasil koneksi ke database Supabase")

	err = db.AutoMigrate(
		&models.Merchant{}, // Tambahan Tabel Merchant
		&models.PaymentGateway{},
		&models.Transaction{},
		&models.Admin{},
	)

	if err != nil {
		log.Printf("⚠️ Gagal migrasi otomatis: %v", err)
	} else {
		log.Println("✅ Tabel database siap digunakan")
	}

	return db
}
