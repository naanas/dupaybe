package repository

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitDB(cfg *config.Config) *gorm.DB {
	dsn := cfg.DatabaseURL
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})

	if err != nil {
		log.Fatalf("❌ Gagal koneksi ke database: %v", err)
	}

	log.Println("✅ Berhasil koneksi ke database Supabase")

	// AutoMigrate akan membuat tabel 'transactions' secara otomatis jika belum ada!
	err = db.AutoMigrate(&models.PaymentGateway{}, &models.Transaction{})
	if err != nil {
		log.Fatalf("❌ Gagal melakukan migrasi tabel: %v", err)
	}

	log.Println("✅ Tabel database siap digunakan")

	return db
}
