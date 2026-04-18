package http

import (
	"dupay/internal/models"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

type AdminHandler struct {
	db *gorm.DB
}

func NewAdminHandler(db *gorm.DB) *AdminHandler {
	return &AdminHandler{db: db}
}

func (h *AdminHandler) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Simple hardcoded admin untuk test pertama kali
	// Nanti bisa dipindah ke DB dengan bcrypt
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

// Fitur nambahin PG baru lewat UI
func (h *AdminHandler) CreateGateway(c *gin.Context) {
	var pg models.PaymentGateway
	if err := c.ShouldBindJSON(&pg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.db.Create(&pg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Gateway berhasil ditambahkan", "data": pg})
}

// Lihat daftar PG yang sudah terintegrasi
func (h *AdminHandler) GetGateways(c *gin.Context) {
	var gateways []models.PaymentGateway
	h.db.Find(&gateways)
	c.JSON(http.StatusOK, gateways)
}
