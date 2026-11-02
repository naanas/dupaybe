package http

import (
	"dupay/internal/models"
	"dupay/internal/service"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// FIX: Pastikan nama field-nya 'service' biar pemanggilannya (h.service) nggak error
type ChargeHandler struct {
	service service.ChargeService
}

func NewChargeHandler(s service.ChargeService) *ChargeHandler {
	return &ChargeHandler{service: s}
}

func (h *ChargeHandler) CreateCharge(c *gin.Context) {
	var req models.ChargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Ambil merchant_id dari context (yang disuntik oleh APISecurityMiddleware)
	merchantID := c.GetString("merchant_id")
	idempotencyKey := c.GetHeader("X-Idempotency-Key")

	trx, err := h.service.ProcessCharge(&req, idempotencyKey, merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Charge routed successfully",
		"data":    trx,
	})
}

func (h *ChargeHandler) GetChargeStatus(c *gin.Context) {
	id := c.Param("id")
	trx, err := h.service.GetTransaction(id)
	if err != nil || trx.MerchantID != c.GetString("merchant_id") {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trx})
}

// SyncChargeStatus: merchant minta Dupay ngecek status transaksi langsung ke PG
// (buat kasus webhook PG telat/nggak nyampe). Cuma bisa buat transaksi milik merchant itu sendiri.
func (h *ChargeHandler) SyncChargeStatus(c *gin.Context) {
	h.syncStatus(c, c.GetString("merchant_id"))
}

// SyncTransactionCMS: sama kayak SyncChargeStatus, dipanggil dari CMS buat transaksi mana aja.
func (h *ChargeHandler) SyncTransactionCMS(c *gin.Context) {
	h.syncStatus(c, "")
}

func (h *ChargeHandler) syncStatus(c *gin.Context, merchantID string) {
	trx, pgStatus, err := h.service.SyncStatus(c.Param("id"), merchantID)
	if err != nil {
		code := http.StatusBadGateway
		if err.Error() == "transaction not found" {
			code = http.StatusNotFound
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trx, "pg_status": pgStatus})
}

// FetchGatewayChannels (CMS): narik daftar channel dari PG pakai isi form gateway (boleh belum disimpan)
// lalu digabung sama channel_mapping di form. Cuma preview, nggak disimpan sampai admin save gateway-nya.
func (h *ChargeHandler) FetchGatewayChannels(c *gin.Context) {
	var req struct {
		Gateway models.PaymentGateway `json:"gateway"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	items, err := h.service.FetchPGChannels(req.Gateway)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// TestGatewayEndpoint (CMS): nembak satu endpoint pakai isi form gateway (boleh belum disimpan)
// & nilai contoh, lalu ngembaliin request/response mentahnya.
func (h *ChargeHandler) TestGatewayEndpoint(c *gin.Context) {
	var req struct {
		Gateway  models.PaymentGateway     `json:"gateway"`
		Endpoint models.GatewayEndpoint    `json:"endpoint"`
		Values   service.EndpointTestInput `json:"values"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Gateway.BaseURL) == "" || strings.TrimSpace(req.Endpoint.Path) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL & path endpoint wajib diisi"})
		return
	}

	result, err := h.service.TestEndpoint(req.Gateway, req.Endpoint, req.Values)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// GetChannels mengembalikan daftar channel AKTIF untuk gateway tertentu.
// Public endpoint (tanpa auth) — dipake frontend buat render dynamic payment options.
//
// Contoh: GET /v1/charge/channels?gateway=TripayProd
func (h *ChargeHandler) GetChannels(c *gin.Context) {
	gatewayName := c.Query("gateway")

	channels, err := h.service.GetPublicChannels(gatewayName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   channels,
	})
}

func (h *ChargeHandler) HandleWebhook(c *gin.Context) {
	gatewayName := c.Param("gateway_name")

	// 1. Baca Raw Body (Ini yang akan di-hash HMAC)
	payloadBytes, _ := io.ReadAll(c.Request.Body)
	payloadStr := string(payloadBytes)

	// 2. Cari signature dari Header (Tripay pakai X-Callback-Signature)
	signature := c.GetHeader("X-Callback-Signature")
	if signature == "" {
		// Fallback kalau gateway lain pakai nama header beda
		signature = c.GetHeader("X-Signature")
	}

	// 3. Lempar ke Service untuk dicek keamanan dan diupdate statusnya
	err := h.service.ProcessWebhook(gatewayName, payloadStr, signature)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	// 4. Wajib bales 200 OK pakai JSON sukses biar Tripay seneng
	c.JSON(http.StatusOK, gin.H{"success": true})
}
