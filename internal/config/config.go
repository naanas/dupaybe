package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port             string
	GinMode          string
	DatabaseURL      string
	AppEncryptionKey string
}

// LoadConfig membaca file .env dan mengembalikan struct Config
func LoadConfig() *Config {
	// Membaca file .env di root direktori
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found. Using OS environment variables instead.")
	}

	return &Config{
		Port:             getEnv("PORT", "8080"),
		GinMode:          getEnv("GIN_MODE", "debug"),
		DatabaseURL:      getEnv("DATABASE_URL", ""),
		AppEncryptionKey: getEnv("APP_ENCRYPTION_KEY", ""),
	}
}

// getEnv adalah fungsi helper untuk mengambil nilai env atau fallback ke nilai default
func getEnv(key string, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
