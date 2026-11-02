package middleware

import (
	"dupay/internal/models"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Key context yang diisi JWTAuthMiddleware / LoadMerchant.
const (
	CtxUserID     = "user_id"
	CtxRole       = "role"
	CtxMerchantID = "merchant_id"
	CtxMerchant   = "merchant"
)

// JWTAuthMiddleware ngecek token login CMS & nyimpen user_id, role, merchant_id ke context.
func JWTAuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Silakan login dulu"})
			return
		}

		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Sesi login habis, silakan login ulang"})
			return
		}

		role, _ := claims["role"].(string)
		if role == "" {
			// Token dari versi lama (sebelum ada role) cuma dimiliki admin.
			role = models.RoleAdmin
		}
		userID, _ := claims["sub"].(string)
		merchantID, _ := claims["merchant_id"].(string)
		c.Set(CtxUserID, userID)
		c.Set(CtxRole, role)
		c.Set(CtxMerchantID, merchantID)
		c.Next()
	}
}

// RequireRole nolak request kalau role user bukan salah satu yang diizinkan.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString(CtxRole)
		for _, r := range roles {
			if r == role {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Akun lu nggak punya akses ke menu ini"})
	}
}

// LoadMerchant ngambil merchant milik user yang login (role merchant) & nolak kalau merchant-nya nonaktif.
func LoadMerchant(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var merchant models.Merchant
		id := c.GetString(CtxMerchantID)
		if id == "" || db.Where("id = ?", id).First(&merchant).Error != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Merchant untuk akun ini nggak ditemukan"})
			return
		}
		if !merchant.IsActive {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Merchant lu lagi dinonaktifkan. Hubungi admin Dupay."})
			return
		}
		c.Set(CtxMerchant, &merchant)
		c.Next()
	}
}
