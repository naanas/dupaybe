package http

import (
	"dupay/internal/models"
	"dupay/internal/service"
	"io"
	"net/http"

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
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trx})
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
