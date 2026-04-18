package middleware

import (
	"bytes"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Menambahkan dependensi db ke dalam middleware
func APISecurityMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		signature := c.GetHeader("X-Signature")
		timestamp := c.GetHeader("X-Timestamp")

		if apiKey == "" || signature == "" || timestamp == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing security headers"})
			return
		}

		// 1. Cari Merchant di Database berdasarkan API Key
		var merchant models.Merchant
		if err := db.Where("api_key = ? AND is_active = ?", apiKey, true).First(&merchant).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key or Inactive Merchant"})
			return
		}

		// Baca body
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 2. Verifikasi Signature menggunakan SecretKey asli milik Merchant tersebut
		if !crypto.VerifySignature(string(bodyBytes), timestamp, merchant.SecretKey, signature) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid digital signature"})
			return
		}

		// 3. Sisipkan ID Merchant ke dalam Context agar bisa dipakai oleh handler
		c.Set("merchant_id", merchant.ID)

		c.Next()
	}
}
