package http

import (
	"dupay/internal/models"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,50}$`)

// Register = daftar merchant baru dari halaman publik. Merchant dibikin nonaktif:
// API Key & Secret Key udah ada, tapi baru bisa dipakai (dan login) setelah admin mengaktifkan.
func (h *AdminHandler) Register(c *gin.Context) {
	var req struct {
		BusinessName string `json:"business_name"`
		Email        string `json:"email"`
		PICName      string `json:"pic_name"`
		Phone        string `json:"phone"`
		Username     string `json:"username"`
		Password     string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data pendaftaran nggak valid"})
		return
	}
	req.BusinessName = strings.TrimSpace(req.BusinessName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.PICName = strings.TrimSpace(req.PICName)
	req.Phone = strings.TrimSpace(req.Phone)
	req.Username = strings.TrimSpace(req.Username)

	switch {
	case len(req.BusinessName) < 3:
		c.JSON(http.StatusBadRequest, gin.H{"error": "nama bisnis minimal 3 karakter"})
		return
	case req.PICName == "":
		c.JSON(http.StatusBadRequest, gin.H{"error": "nama PIC wajib diisi"})
		return
	case !usernamePattern.MatchString(req.Username):
		c.JSON(http.StatusBadRequest, gin.H{"error": "username 3-50 karakter, cuma huruf, angka, titik, strip, underscore"})
		return
	case len(req.Password) < 8:
		c.JSON(http.StatusBadRequest, gin.H{"error": "password minimal 8 karakter"})
		return
	}
	if addr, err := mail.ParseAddress(req.Email); err != nil || addr.Address != req.Email {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format email nggak valid"})
		return
	}

	var count int64
	if h.db.Model(&models.Merchant{}).Where("lower(name) = lower(?)", req.BusinessName).Count(&count); count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "nama bisnis udah terdaftar"})
		return
	}
	if h.db.Model(&models.Merchant{}).Where("lower(email) = ?", req.Email).Count(&count); count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "email udah terdaftar"})
		return
	}
	if h.db.Model(&models.User{}).Where("username = ?", req.Username).Count(&count); count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "username udah dipakai"})
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan password"})
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		merchant := models.Merchant{
			Name:      req.BusinessName,
			Email:     req.Email,
			Phone:     req.Phone,
			PICName:   req.PICName,
			APIKey:    "pk_" + uuid.New().String(),
			SecretKey: "sk_" + uuid.New().String(),
		}
		if err := tx.Create(&merchant).Error; err != nil {
			return err
		}
		// is_active punya default:true di GORM, jadi false harus di-update terpisah.
		if err := tx.Model(&merchant).Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Create(&models.User{
			Username:   req.Username,
			Password:   string(hashed),
			Role:       models.RoleMerchant,
			MerchantID: &merchant.ID,
		}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal mendaftar, coba lagi"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Pendaftaran berhasil. Tunggu admin Dupay mengaktifkan akun lu."})
}

// SetMerchantActive nyalain / matiin merchant (dipakai juga buat menyetujui pendaftaran). Body: {"is_active": bool}.
func (h *AdminHandler) SetMerchantActive(c *gin.Context) {
	var body struct {
		IsActive *bool `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.IsActive == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "is_active wajib diisi"})
		return
	}
	res := h.db.Model(&models.Merchant{}).Where("id = ?", c.Param("id")).Update("is_active", *body.IsActive)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal mengubah status merchant"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant tidak ditemukan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"is_active": *body.IsActive})
}
