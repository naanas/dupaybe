package middleware

import (
	"bytes"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func APISecurityMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		signature := c.GetHeader("X-Signature")
		timestamp := c.GetHeader("X-Timestamp")

		if apiKey == "" || signature == "" || timestamp == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing security headers"})
			return
		}

		// 1. Cari Merchant berdasarkan API Key
		var merchant models.Merchant
		if err := db.Where("api_key = ? AND is_active = ?", apiKey, true).First(&merchant).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key or Inactive Merchant"})
			return
		}

		// 2. LAYER KEAMANAN: IP WHITELISTING
		// Jika database merchant punya catatan IP, kita wajib cek IP penembak
		if merchant.WhitelistedIPs != "" {
			clientIP := c.ClientIP() // Gin otomatis mengambil IP asli klien dari Header/Socket
			isAllowed := false

			// Pisahkan berdasarkan koma (karena bisa jadi 1 merchant punya >1 server/IP)
			allowedIPs := strings.Split(merchant.WhitelistedIPs, ",")
			for _, ip := range allowedIPs {
				if strings.TrimSpace(ip) == clientIP {
					isAllowed = true
					break
				}
			}

			// Tolak jika IP tidak terdaftar
			if !isAllowed {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "FORBIDDEN: Alamat IP Anda (" + clientIP + ") tidak diizinkan untuk mengakses API Key ini",
				})
				return
			}
		}

		// Baca body
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 3. Verifikasi Digital Signature HMAC
		if !crypto.VerifySignature(string(bodyBytes), timestamp, merchant.SecretKey, signature) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid digital signature"})
			return
		}

		// 4. Sisipkan ID Merchant ke dalam Context
		c.Set("merchant_id", merchant.ID)

		c.Next()
	}
}
