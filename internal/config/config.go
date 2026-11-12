package config

import (
	"crypto/rand"
	"encoding/hex"
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
	if len(jwtSecret) < 32 {
		// Secret default yang ketulis di kode = siapa pun bisa bikin token admin CMS sendiri.
		if os.Getenv("GIN_MODE") == "release" || os.Getenv("K_SERVICE") != "" {
			log.Fatal("JWT_SECRET wajib diisi (minimal 32 karakter) di production. Contoh: openssl rand -hex 32")
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			log.Fatalf("gagal bikin JWT secret: %v", err)
		}
		jwtSecret = hex.EncodeToString(buf)
		log.Println("Warning: JWT_SECRET kosong/pendek, pakai secret acak sementara (login CMS hilang tiap restart). Isi JWT_SECRET di .env.")
	}
	if len(os.Getenv("APP_ENCRYPTION_KEY")) != 16 && len(os.Getenv("APP_ENCRYPTION_KEY")) != 24 && len(os.Getenv("APP_ENCRYPTION_KEY")) != 32 {
		log.Println("Warning: APP_ENCRYPTION_KEY bukan 16/24/32 byte — key gateway nggak bisa didekripsi & callback akan ditolak.")
	}

	return &Config{
		// Pastikan di file .env kamu ada variabel DATABASE_URL dan APP_ENCRYPTION_KEY
		DBURL:            os.Getenv("DATABASE_URL"),
		AppEncryptionKey: os.Getenv("APP_ENCRYPTION_KEY"),
		JWTSecret:        jwtSecret,
	}
}
