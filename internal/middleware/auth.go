package middleware

import (
	"bytes"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxClockSkew = 5 * time.Minute

func freshTimestamp(ts string, now time.Time) bool {
	n, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil || n <= 0 {
		return false
	}
	if n > 1e12 { // milidetik
		n /= 1000
	}
	d := now.Sub(time.Unix(n, 0))
	return d <= maxClockSkew && d >= -maxClockSkew
}

func APISecurityMiddleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		signature := c.GetHeader("X-Signature")
		timestamp := c.GetHeader("X-Timestamp")

		if apiKey == "" || signature == "" || timestamp == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing security headers"})
			return
		}

		// Tolak request lama (anti-replay): X-Timestamp = unix detik (atau milidetik), toleransi ±5 menit.
		if !freshTimestamp(timestamp, time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "X-Timestamp kedaluwarsa atau tidak valid (maks selisih 5 menit dari waktu server)"})
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
					"error": "FORBIDDEN: Alamat IP Anda (" + clientIP + ") tidak diizinkan untuk API Key ini. Tambahkan IP ini ke whitelist merchant di Dupay CMS (Whitelisted IPs).",
				})
				return
			}
		}

		// Baca body
		bodyBytes, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
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
