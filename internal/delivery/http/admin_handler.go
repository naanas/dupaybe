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
	"golang.org/x/crypto/bcrypt"
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

	var admin models.Admin
	if err := h.db.Where("username = ?", req.Username).First(&admin).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username atau password salah"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username atau password salah"})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": admin.Username,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	tokenString, _ := token.SignedString([]byte("DUPAY_CMS_SECRET_KEY_2024"))
	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

func (h *AdminHandler) CreateGateway(c *gin.Context) {
	var pg models.PaymentGateway
	if err := c.ShouldBindJSON(&pg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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

// --- FITUR MERCHANT ---
func (h *AdminHandler) CreateMerchant(c *gin.Context) {
	var req struct {
		Name           string `json:"name" binding:"required"`
		Email          string `json:"email" binding:"required"`
		Phone          string `json:"phone"`
		PICName        string `json:"pic_name"`
		WhitelistedIPs string `json:"whitelisted_ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	merchant := models.Merchant{
		Name:           req.Name,
		Email:          req.Email,
		Phone:          req.Phone,
		PICName:        req.PICName,
		APIKey:         "pk_" + uuid.New().String(),
		SecretKey:      "sk_" + uuid.New().String(),
		WhitelistedIPs: req.WhitelistedIPs,
		IsActive:       true,
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
func (h *AdminHandler) UpdateMerchant(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name           string `json:"name"`
		Email          string `json:"email"`
		Phone          string `json:"phone"`
		PICName        string `json:"pic_name"`
		WhitelistedIPs string `json:"whitelisted_ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var merchant models.Merchant
	if err := h.db.Where("id = ?", id).First(&merchant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant tidak ditemukan"})
		return
	}

	// Update data
	merchant.Name = req.Name
	merchant.Email = req.Email
	merchant.Phone = req.Phone
	merchant.PICName = req.PICName
	merchant.WhitelistedIPs = req.WhitelistedIPs

	if err := h.db.Save(&merchant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil diupdate", "data": merchant})
}

func (h *AdminHandler) DeleteMerchant(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Where("id = ?", id).Delete(&models.Merchant{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil dihapus"})
}
