package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBURL            string
	AppEncryptionKey string
	JWTSecret        string
}

func LoadConfig() *Config {
	// Load variabel dari file .env (jika ada)
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		// Nilai lama biar token yang udah ada tetap jalan. Isi JWT_SECRET di .env buat production.
		jwtSecret = "DUPAY_CMS_SECRET_KEY_2024"
		log.Println("Warning: JWT_SECRET kosong, pakai secret default. Isi JWT_SECRET di .env sebelum production!")
	}

	return &Config{
		// Pastikan di file .env kamu ada variabel DATABASE_URL dan APP_ENCRYPTION_KEY
		DBURL:            os.Getenv("DATABASE_URL"),
		AppEncryptionKey: os.Getenv("APP_ENCRYPTION_KEY"),
		JWTSecret:        jwtSecret,
	}
}
