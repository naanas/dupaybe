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
	// Pastikan DATABASE_URL di .env pakai port 5432 (Session Mode)
	dsn := cfg.DatabaseURL

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// Tambahkan logger biar kita bisa liat SQL mana yang error
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatalf("❌ Gagal koneksi ke database: %v", err)
	}

	log.Println("✅ Berhasil koneksi ke database Supabase")

	// PENTING: Karena database lo udah ada isinya tapi strukturnya bentrok,
	// AutoMigrate bakal terus-terusan error SQLSTATE 42P07.
	// Hapus tabel sekali lagi lewat SQL Editor Supabase: DROP TABLE IF EXISTS payment_gateways CASCADE;

	err = db.AutoMigrate(
		&models.PaymentGateway{},
		&models.Transaction{},
		&models.Admin{},
	)

	if err != nil {
		log.Printf("⚠️ Gagal migrasi otomatis: %v", err)
		log.Println("💡 Tip: Jalankan 'DROP TABLE IF EXISTS payment_gateways, transactions CASCADE;' di dashboard Supabase")
	} else {
		log.Println("✅ Tabel database siap digunakan")
	}

	return db
}
