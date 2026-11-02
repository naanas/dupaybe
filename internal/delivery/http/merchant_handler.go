package http

import (
	"dupay/internal/middleware"
	"dupay/internal/models"
	"dupay/internal/service"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// MerchantPortalHandler = API buat merchant yang login ke CMS. Semua data dibatasi ke merchant-nya
// sendiri, dan nggak ada info payment gateway yang ikut keluar.
type MerchantPortalHandler struct {
	db      *gorm.DB
	service service.ChargeService
}

func NewMerchantPortalHandler(db *gorm.DB, s service.ChargeService) *MerchantPortalHandler {
	return &MerchantPortalHandler{db: db, service: s}
}

// merchantTransaction = transaksi versi merchant: tanpa gateway, request/response mentah PG.
type merchantTransaction struct {
	ID              string    `json:"id"`
	OrderID         string    `json:"order_id"`
	Amount          float64   `json:"amount"`
	PaymentMethod   string    `json:"payment_method"`
	Status          string    `json:"status"`
	Reference       string    `json:"reference"`
	PaymentCode     string    `json:"payment_code"`
	CheckoutURL     string    `json:"checkout_url"`
	PaymentDeeplink string    `json:"payment_deeplink"`
	CreatedAt       time.Time `json:"created_at"`
}

func toMerchantTransaction(t models.Transaction) merchantTransaction {
	return merchantTransaction{
		ID:              t.ID,
		OrderID:         t.OrderID,
		Amount:          t.Amount,
		PaymentMethod:   t.PaymentMethod,
		Status:          t.Status,
		Reference:       t.PGReferenceID,
		PaymentCode:     t.PaymentCode,
		CheckoutURL:     t.CheckoutURL,
		PaymentDeeplink: t.PaymentDeeplink,
		CreatedAt:       t.CreatedAt,
	}
}

func currentMerchant(c *gin.Context) *models.Merchant {
	m, _ := c.Get(middleware.CtxMerchant)
	merchant, _ := m.(*models.Merchant)
	return merchant
}

func merchantProfile(m *models.Merchant) gin.H {
	return gin.H{
		"id":              m.ID,
		"name":            m.Name,
		"email":           m.Email,
		"phone":           m.Phone,
		"pic_name":        m.PICName,
		"api_key":         m.APIKey,
		"secret_key":      m.SecretKey,
		"webhook_url":     m.WebhookURL,
		"whitelisted_ips": m.WhitelistedIPs,
	}
}

func (h *MerchantPortalHandler) GetProfile(c *gin.Context) {
	c.JSON(http.StatusOK, merchantProfile(currentMerchant(c)))
}

// UpdateProfile: merchant cuma boleh ngubah kontak & setting integrasi, bukan nama/email/status.
func (h *MerchantPortalHandler) UpdateProfile(c *gin.Context) {
	var req struct {
		Phone          string `json:"phone"`
		PICName        string `json:"pic_name"`
		WebhookURL     string `json:"webhook_url"`
		WhitelistedIPs string `json:"whitelisted_ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	webhook := strings.TrimSpace(req.WebhookURL)
	if webhook != "" && !strings.HasPrefix(webhook, "http://") && !strings.HasPrefix(webhook, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Webhook URL harus diawali http:// atau https://"})
		return
	}

	m := currentMerchant(c)
	m.Phone = strings.TrimSpace(req.Phone)
	m.PICName = strings.TrimSpace(req.PICName)
	m.WebhookURL = webhook
	m.WhitelistedIPs = strings.TrimSpace(req.WhitelistedIPs)
	if err := h.db.Model(m).Select("phone", "pic_name", "webhook_url", "whitelisted_ips").Updates(m).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan profil"})
		return
	}
	c.JSON(http.StatusOK, merchantProfile(m))
}

// RotateSecret bikin Secret Key baru. Secret lama langsung nggak berlaku.
func (h *MerchantPortalHandler) RotateSecret(c *gin.Context) {
	m := currentMerchant(c)
	m.SecretKey = "sk_" + uuid.New().String()
	if err := h.db.Model(m).Update("secret_key", m.SecretKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal membuat secret key baru"})
		return
	}
	c.JSON(http.StatusOK, merchantProfile(m))
}

// GetSummary = angka ringkasan buat halaman Ringkasan merchant.
func (h *MerchantPortalHandler) GetSummary(c *gin.Context) {
	m := currentMerchant(c)
	type row struct {
		Status string
		Count  int64
		Total  float64
	}
	var rows []row
	if err := h.db.Model(&models.Transaction{}).
		Select("status, COUNT(*) AS count, COALESCE(SUM(amount), 0) AS total").
		Where("merchant_id = ?", m.ID).
		Group("status").Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal membaca ringkasan"})
		return
	}

	byStatus := gin.H{"PENDING": 0, "SUCCESS": 0, "FAILED": 0, "REFUNDED": 0}
	var total int64
	var successAmount float64
	for _, r := range rows {
		byStatus[r.Status] = r.Count
		total += r.Count
		if r.Status == "SUCCESS" {
			successAmount = r.Total
		}
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var today int64
	h.db.Model(&models.Transaction{}).
		Where("merchant_id = ? AND created_at >= ?", m.ID, startOfDay).
		Count(&today)

	c.JSON(http.StatusOK, gin.H{
		"total_transactions": total,
		"today_transactions": today,
		"success_amount":     successAmount,
		"by_status":          byStatus,
	})
}

// GetTransactions: transaksi milik merchant ini. Query opsional: status, q (order_id), limit (maks 200).
func (h *MerchantPortalHandler) GetTransactions(c *gin.Context) {
	m := currentMerchant(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	q := h.db.Where("merchant_id = ?", m.ID)
	if status := strings.ToUpper(strings.TrimSpace(c.Query("status"))); status != "" {
		q = q.Where("status = ?", status)
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		q = q.Where("order_id ILIKE ?", "%"+search+"%")
	}

	var trxs []models.Transaction
	if err := q.Order("created_at DESC").Limit(limit).Find(&trxs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal membaca transaksi"})
		return
	}
	out := make([]merchantTransaction, 0, len(trxs))
	for _, t := range trxs {
		out = append(out, toMerchantTransaction(t))
	}
	c.JSON(http.StatusOK, out)
}

// SyncTransaction: cek ulang status ke PG buat transaksi milik merchant ini.
func (h *MerchantPortalHandler) SyncTransaction(c *gin.Context) {
	trx, _, err := h.service.SyncStatus(c.Param("id"), currentMerchant(c).ID)
	if err != nil {
		code := http.StatusBadGateway
		if err.Error() == "transaction not found" {
			code = http.StatusNotFound
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toMerchantTransaction(*trx))
}

// ChangePassword dipakai semua role buat ganti password sendiri. Body: {"old_password", "new_password"}.
func ChangePassword(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			OldPassword string `json:"old_password" binding:"required"`
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password lama & baru wajib diisi"})
			return
		}
		if len(req.NewPassword) < 8 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password baru minimal 8 karakter"})
			return
		}

		var user models.User
		if err := db.Where("id = ?", c.GetString(middleware.CtxUserID)).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "akun nggak ditemukan, silakan login ulang"})
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password lama salah"})
			return
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan password"})
			return
		}
		if err := db.Model(&user).Update("password", string(hashed)).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan password"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Password berhasil diganti"})
	}
}
