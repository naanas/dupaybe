package http

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewAdminHandler(db *gorm.DB, cfg *config.Config) *AdminHandler {
	return &AdminHandler{db: db, cfg: cfg}
}

func (h *AdminHandler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Username == "admin" && req.Password == "dupay123" {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"username": req.Username,
			"exp":      time.Now().Add(time.Hour * 24).Unix(),
		})

		tokenString, _ := token.SignedString([]byte("DUPAY_CMS_SECRET_KEY_2024"))
		c.JSON(http.StatusOK, gin.H{"token": tokenString})
		return
	}

	c.JSON(http.StatusUnauthorized, gin.H{"error": "Username atau password salah"})
}

// --- GATEWAY MANAGEMENT ---

func (h *AdminHandler) CreateGateway(c *gin.Context) {
	var pg models.PaymentGateway
	if err := c.ShouldBindJSON(&pg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ENKRIPSI SERVER KEY sebelum masuk ke Database (AES-256)
	if pg.ServerKey != "" {
		encryptedKey, err := crypto.EncryptAES([]byte(h.cfg.AppEncryptionKey), pg.ServerKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengenkripsi kredensial gateway"})
			return
		}
		pg.ServerKey = encryptedKey
	}

	if err := h.db.Create(&pg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Gateway berhasil ditambahkan", "data": pg})
}

func (h *AdminHandler) GetGateways(c *gin.Context) {
	var gateways []models.PaymentGateway
	h.db.Find(&gateways)
	c.JSON(http.StatusOK, gateways)
}

// --- MERCHANT / CLIENT MANAGEMENT ---

func (h *AdminHandler) CreateMerchant(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Generate Kredensial Unik untuk Client Baru
	merchant := models.Merchant{
		Name:      req.Name,
		APIKey:    "pk_" + uuid.New().String(),
		SecretKey: "sk_" + uuid.New().String(),
		IsActive:  true,
	}

	if err := h.db.Create(&merchant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil dibuat", "data": merchant})
}

func (h *AdminHandler) GetMerchants(c *gin.Context) {
	var merchants []models.Merchant
	h.db.Find(&merchants)
	c.JSON(http.StatusOK, merchants)
}
