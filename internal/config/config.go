package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBURL            string
	AppEncryptionKey string
}

func LoadConfig() *Config {
	// Load variabel dari file .env (jika ada)
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}

	return &Config{
		// Pastikan di file .env kamu ada variabel DATABASE_URL dan APP_ENCRYPTION_KEY
		DBURL:            os.Getenv("DATABASE_URL"),
		AppEncryptionKey: os.Getenv("APP_ENCRYPTION_KEY"),
	}
}
