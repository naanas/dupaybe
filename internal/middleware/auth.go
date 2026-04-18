package middleware

import (
	"bytes"
	"dupay/pkg/crypto"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

func APISecurityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-KEY")
		signature := c.GetHeader("X-Signature")
		timestamp := c.GetHeader("X-Timestamp")

		if apiKey == "" || signature == "" || timestamp == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing security headers"})
			return
		}

		secretKey := "super-secret-key-from-db" // Simulasi ambil DB
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		if !crypto.VerifySignature(string(bodyBytes), timestamp, secretKey, signature) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid digital signature"})
			return
		}

		c.Next()
	}
}
