package http

import (
	"dupay/internal/config"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
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

	var user models.User
	if err := h.db.Where("username = ?", strings.TrimSpace(req.Username)).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username atau password salah"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username atau password salah"})
		return
	}

	merchantID, merchantName := "", ""
	if user.Role == models.RoleMerchant {
		var merchant models.Merchant
		if user.MerchantID == nil || h.db.Where("id = ?", *user.MerchantID).First(&merchant).Error != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Merchant untuk akun ini nggak ditemukan"})
			return
		}
		if !merchant.IsActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "Akun merchant lu belum aktif (nunggu persetujuan) atau lagi dinonaktifkan. Hubungi admin Dupay."})
			return
		}
		merchantID, merchantName = merchant.ID, merchant.Name
	} else if user.Role != models.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Role akun nggak dikenal"})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":         user.ID,
		"username":    user.Username,
		"role":        user.Role,
		"merchant_id": merchantID,
		"exp":         time.Now().Add(time.Hour * 24).Unix(),
	})
	tokenString, err := token.SignedString([]byte(h.cfg.JWTSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal membuat token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": tokenString,
		"user": gin.H{
			"username":      user.Username,
			"role":          user.Role,
			"merchant_id":   merchantID,
			"merchant_name": merchantName,
		},
	})
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

	endpoints, err := normalizeEndpoints(pg.Endpoints)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pg.Endpoints = nil

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pg).Error; err != nil {
			return err
		}
		return replaceEndpoints(tx, pg.ID, endpoints)
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	pg.Endpoints = endpoints
	c.JSON(http.StatusOK, gin.H{"message": "Gateway berhasil ditambahkan", "data": pg})
}

func (h *AdminHandler) GetGateways(c *gin.Context) {
	var gateways []models.PaymentGateway
	h.db.Preload("Endpoints", func(db *gorm.DB) *gorm.DB {
		return db.Order("action, channel_method")
	}).Order("priority, name").Find(&gateways)
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

	if pg.RequestTemplate == "" {
		pg.RequestTemplate = "{}"
	}
	if pg.ResponseMapping == "" {
		pg.ResponseMapping = "{}"
	}
	if pg.WebhookMapping == "" {
		pg.WebhookMapping = "{}"
	}

	// endpoints nggak dikirim (null) = endpoint lama dibiarin; array (termasuk []) = diganti semua.
	replace := pg.Endpoints != nil
	endpoints, err := normalizeEndpoints(pg.Endpoints)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pg.Endpoints = nil

	pg.ID = id
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&pg).Error; err != nil {
			return err
		}
		if !replace {
			return nil
		}
		return replaceEndpoints(tx, id, endpoints)
	}); err != nil {
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
		WebhookURL     string `json:"webhook_url"`
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
		WebhookURL:     req.WebhookURL,
		IsActive:       true,
	}

	if err := h.db.Create(&merchant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil dibuat", "data": merchant})
}

type merchantWithLogin struct {
	models.Merchant
	LoginUsername string `json:"login_username"` // kosong = merchant belum punya akun login CMS
}

func (h *AdminHandler) GetMerchants(c *gin.Context) {
	var merchants []models.Merchant
	h.db.Order("name").Find(&merchants)

	var users []models.User
	h.db.Where("role = ? AND merchant_id IS NOT NULL", models.RoleMerchant).Find(&users)
	logins := map[string]string{}
	for _, u := range users {
		logins[*u.MerchantID] = u.Username
	}

	out := make([]merchantWithLogin, 0, len(merchants))
	for _, m := range merchants {
		out = append(out, merchantWithLogin{Merchant: m, LoginUsername: logins[m.ID]})
	}
	c.JSON(http.StatusOK, out)
}

// SetMerchantLogin bikin / ganti akun login CMS buat merchant. Body: {"username", "password"}.
// Password kosong = cuma ganti username (akun harus udah ada).
func (h *AdminHandler) SetMerchantLogin(c *gin.Context) {
	merchantID := c.Param("id")
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username wajib diisi"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Password != "" && len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password minimal 8 karakter"})
		return
	}

	var merchant models.Merchant
	if err := h.db.Where("id = ?", merchantID).First(&merchant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merchant tidak ditemukan"})
		return
	}

	var taken models.User
	if h.db.Where("username = ? AND (merchant_id IS NULL OR merchant_id <> ?)", req.Username, merchantID).First(&taken).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username udah dipakai akun lain"})
		return
	}

	var user models.User
	exists := h.db.Where("merchant_id = ?", merchantID).First(&user).Error == nil
	if !exists && req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password wajib diisi buat akun baru"})
		return
	}

	user.Username = req.Username
	user.Role = models.RoleMerchant
	user.MerchantID = &merchant.ID
	if req.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan password"})
			return
		}
		user.Password = string(hashed)
	}
	if err := h.db.Save(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan akun login"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Akun login merchant tersimpan", "login_username": user.Username})
}

// DeleteMerchantLogin hapus akun login CMS merchant (API Key-nya tetap jalan).
func (h *AdminHandler) DeleteMerchantLogin(c *gin.Context) {
	if err := h.db.Where("merchant_id = ? AND role = ?", c.Param("id"), models.RoleMerchant).Delete(&models.User{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menghapus akun login"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Akun login merchant dihapus"})
}

func (h *AdminHandler) UpdateMerchant(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name           string `json:"name" binding:"required"`
		Email          string `json:"email" binding:"required"`
		Phone          string `json:"phone"`
		PICName        string `json:"pic_name"`
		WhitelistedIPs string `json:"whitelisted_ips"`
		WebhookURL     string `json:"webhook_url"`
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
	merchant.WebhookURL = req.WebhookURL

	if err := h.db.Save(&merchant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil diupdate", "data": merchant})
}

func (h *AdminHandler) DeleteMerchant(c *gin.Context) {
	id := c.Param("id")
	// Akun login merchant ikut dihapus (di DB lama FK-nya belum cascade).
	h.db.Where("merchant_id = ?", id).Delete(&models.User{})
	if err := h.db.Where("id = ?", id).Delete(&models.Merchant{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Merchant berhasil dihapus"})
}

var placeholderPattern = regexp.MustCompile(`\{\{[^}]*\}\}`)

// normalizeEndpoints validasi & rapihin endpoint kiriman CMS sebelum disimpan.
func normalizeEndpoints(in []models.GatewayEndpoint) ([]models.GatewayEndpoint, error) {
	validChannelMethods := map[string]bool{"": true, "va": true, "qris": true, "ewallet": true, "cstore": true}
	seen := map[string]bool{}
	seenNames := map[string]bool{}
	out := make([]models.GatewayEndpoint, 0, len(in))

	for i, ep := range in {
		n := i + 1
		ep.ID = ""
		ep.Name = strings.TrimSpace(ep.Name)
		ep.Action = strings.ToLower(strings.TrimSpace(ep.Action))
		ep.ChannelMethod = strings.ToLower(strings.TrimSpace(ep.ChannelMethod))
		ep.HTTPMethod = strings.ToUpper(strings.TrimSpace(ep.HTTPMethod))
		ep.Path = strings.TrimSpace(ep.Path)
		ep.RequestTemplate = strings.TrimSpace(ep.RequestTemplate)
		ep.ResponseMapping = strings.TrimSpace(ep.ResponseMapping)
		if ep.HTTPMethod == "" {
			ep.HTTPMethod = http.MethodPost
		}

		if ep.Name == "" {
			return nil, fmt.Errorf("endpoint #%d: nama wajib diisi", n)
		}
		if len(ep.Name) > 100 {
			return nil, fmt.Errorf("endpoint %q: nama maksimal 100 karakter", ep.Name)
		}
		if seenNames[strings.ToLower(ep.Name)] {
			return nil, fmt.Errorf("nama endpoint %q dipakai lebih dari sekali", ep.Name)
		}
		seenNames[strings.ToLower(ep.Name)] = true

		switch ep.Action {
		case models.EndpointActionCharge, models.EndpointActionStatus:
		case "", models.EndpointActionChannels:
			ep.ChannelMethod = "" // custom & daftar channel nggak dibedain per jenis channel
		default:
			return nil, fmt.Errorf("endpoint %q: dipakai untuk harus kosong, charge, status, atau channels", ep.Name)
		}
		if ep.HTTPMethod != http.MethodGet && ep.HTTPMethod != http.MethodPost {
			return nil, fmt.Errorf("endpoint %q: http method harus GET atau POST", ep.Name)
		}
		if !validChannelMethods[ep.ChannelMethod] {
			return nil, fmt.Errorf("endpoint %q: channel method %q tidak dikenal", ep.Name, ep.ChannelMethod)
		}
		if ep.Path == "" {
			return nil, fmt.Errorf("endpoint %q: path wajib diisi", ep.Name)
		}
		if ep.HTTPMethod == http.MethodGet {
			ep.RequestTemplate = ""
		}
		// Placeholder tanpa kutip (mis. {"transactionId": {{pg_reference_id}}}) baru jadi JSON setelah diisi,
		// jadi dicek pakai nilai contoh.
		if ep.RequestTemplate != "" && !json.Valid([]byte(placeholderPattern.ReplaceAllString(ep.RequestTemplate, "0"))) {
			return nil, fmt.Errorf("endpoint %q: request template bukan JSON yang valid", ep.Name)
		}
		if ep.ResponseMapping != "" && !json.Valid([]byte(ep.ResponseMapping)) {
			return nil, fmt.Errorf("endpoint %q: response mapping bukan JSON yang valid", ep.Name)
		}

		// Peran otomatis cuma boleh satu per jenis channel; endpoint custom bebas.
		if ep.Action != "" {
			key := ep.Action + "|" + ep.ChannelMethod
			if seen[key] {
				scope := ep.ChannelMethod
				if scope == "" {
					scope = "semua channel"
				}
				return nil, fmt.Errorf("lebih dari satu endpoint dipakai untuk %s (%s)", ep.Action, scope)
			}
			seen[key] = true
		}
		out = append(out, ep)
	}
	return out, nil
}

// replaceEndpoints ganti semua endpoint sebuah gateway dengan daftar baru.
func replaceEndpoints(tx *gorm.DB, gatewayID string, endpoints []models.GatewayEndpoint) error {
	if err := tx.Where("gateway_id = ?", gatewayID).Delete(&models.GatewayEndpoint{}).Error; err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return nil
	}
	for i := range endpoints {
		endpoints[i].GatewayID = gatewayID
	}
	return tx.Create(&endpoints).Error
}

// GetDisabledPaymentMethods = daftar kode metode bayar yang lagi dimatiin.
func (h *AdminHandler) GetDisabledPaymentMethods(c *gin.Context) {
	codes := []string{}
	h.db.Model(&models.DisabledPaymentMethod{}).Order("code").Pluck("code", &codes)
	c.JSON(http.StatusOK, codes)
}

// SetPaymentMethodActive nyalain/matiin satu metode bayar buat semua gateway. Body: {"is_active": bool}.
func (h *AdminHandler) SetPaymentMethodActive(c *gin.Context) {
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	var body struct {
		IsActive *bool `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.IsActive == nil || code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kode & is_active wajib diisi"})
		return
	}

	var err error
	if *body.IsActive {
		err = h.db.Where("code = ?", code).Delete(&models.DisabledPaymentMethod{}).Error
	} else {
		err = h.db.Where(models.DisabledPaymentMethod{Code: code}).FirstOrCreate(&models.DisabledPaymentMethod{}).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gagal menyimpan status metode bayar"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": code, "is_active": *body.IsActive})
}
