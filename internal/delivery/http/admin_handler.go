package http

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/json"
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

type CMSTransactionItem struct {
	ID             string    `json:"id"`
	OrderID        string    `json:"order_id"`
	MerchantID     string    `json:"merchant_id"`
	MerchantName   string    `json:"merchant_name"`
	GatewayID      string    `json:"gateway_id"`
	GatewayName    string    `json:"gateway_name"`
	Amount         float64   `json:"amount"`
	PaymentMethod  string    `json:"payment_method"`
	Status         string    `json:"status"`
	PGReferenceID  string    `json:"pg_reference_id"`
	CheckoutURL    string    `json:"checkout_url"`
	ClientPayload  string    `json:"client_payload"`
	PGResponse     string    `json:"pg_response"`
	PGStatusCode   int       `json:"pg_status_code"`
	CreatedAt      time.Time `json:"created_at"`
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

// ==========================================
// GATEWAY MANAGEMENT (CMS)
// ==========================================
func (h *AdminHandler) CreateGateway(c *gin.Context) {
	var pg models.PaymentGateway
	if err := c.ShouldBindJSON(&pg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if pg.RequestTemplate == "" {
		pg.RequestTemplate = "{}"
	}
	if pg.ResponseMapping == "" {
		pg.ResponseMapping = "{}"
	}
	if pg.WebhookMapping == "" {
		pg.WebhookMapping = "{}"
	}

	if !json.Valid([]byte(pg.RequestTemplate)) || !json.Valid([]byte(pg.WebhookMapping)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format JSON Template/Mapping tidak valid."})
		return
	}

	if pg.ServerKey != "" {
		encryptedKey, err := crypto.EncryptAES([]byte(h.cfg.AppEncryptionKey), pg.ServerKey)
		if err == nil {
			pg.ServerKey = encryptedKey
		}
	}

	if pg.PrivateKey != "" {
		encryptedPriv, err := crypto.EncryptAES([]byte(h.cfg.AppEncryptionKey), pg.PrivateKey)
		if err == nil {
			pg.PrivateKey = encryptedPriv
		}
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

func (h *AdminHandler) UpdateGateway(c *gin.Context) {
	id := c.Param("id")
	var pg models.PaymentGateway
	if err := c.ShouldBindJSON(&pg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.PaymentGateway
	if err := h.db.Where("id = ?", id).First(&existing).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Gateway tidak ditemukan"})
		return
	}

	if pg.ServerKey != "" && pg.ServerKey != existing.ServerKey {
		encryptedKey, _ := crypto.EncryptAES([]byte(h.cfg.AppEncryptionKey), pg.ServerKey)
		pg.ServerKey = encryptedKey
	} else {
		pg.ServerKey = existing.ServerKey
	}

	if pg.PrivateKey != "" && pg.PrivateKey != existing.PrivateKey {
		encryptedPriv, _ := crypto.EncryptAES([]byte(h.cfg.AppEncryptionKey), pg.PrivateKey)
		pg.PrivateKey = encryptedPriv
	} else {
		pg.PrivateKey = existing.PrivateKey
	}

	pg.ID = id
	if err := h.db.Save(&pg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Gateway berhasil diupdate"})
}

func (h *AdminHandler) DeleteGateway(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Where("id = ?", id).Delete(&models.PaymentGateway{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Gateway berhasil dihapus"})
}

func (h *AdminHandler) GetTransactions(c *gin.Context) {
	limit := 100
	var transactions []CMSTransactionItem

	if err := h.db.
		Table("transactions t").
		Select(`
			t.id,
			t.order_id,
			t.merchant_id,
			COALESCE(m.name, '') as merchant_name,
			t.payment_gateway_id as gateway_id,
			COALESCE(pg.name, '') as gateway_name,
			t.amount,
			t.payment_method,
			t.status,
			t.pg_reference_id,
			t.checkout_url,
			t.client_payload,
			t.pg_response,
			t.pg_status_code,
			t.created_at
		`).
		Joins("LEFT JOIN merchants m ON m.id = t.merchant_id").
		Joins("LEFT JOIN payment_gateways pg ON pg.id = t.payment_gateway_id").
		Order("t.created_at DESC").
		Limit(limit).
		Scan(&transactions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, transactions)
}

// ==========================================
// MERCHANT MANAGEMENT (CMS)
// ==========================================
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

	var merchant models.Merchant
	if err := h.db.Where("id = ?", id).First(&merchant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant tidak ditemukan"})
		return
	}

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
