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
	dsn := cfg.DatabaseURL

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})

	if err != nil {
		log.Fatalf("❌ Gagal koneksi ke database: %v", err)
	}

	log.Println("✅ Berhasil koneksi ke database Supabase")

	err = db.AutoMigrate(
		&models.Merchant{},
		&models.PaymentGateway{},
		&models.Transaction{},
		&models.Admin{},
	)

	if err != nil {
		log.Printf("⚠️ Gagal migrasi otomatis: %v", err)
	} else {
		log.Println("✅ Tabel database siap digunakan")
	}

	// FIX: AUTO SEEDER UNTUK ADMIN PERTAMA KALI MENGGUNAKAN BCRYPT
	var adminCount int64
	db.Model(&models.Admin{}).Count(&adminCount)
	if adminCount == 0 {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("dupay123"), bcrypt.DefaultCost)
		db.Create(&models.Admin{
			Username: "admin",
			Password: string(hashedPassword),
		})
		log.Println("🔑 Default Admin created! (Username: admin, Password: dupay123)")
	}

	return db
}
