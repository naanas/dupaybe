package http

import (
	"dupay/internal/models"
	"dupay/internal/service"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ChargeHandler struct {
	chargeService service.ChargeService
}

func NewChargeHandler(cs service.ChargeService) *ChargeHandler {
	return &ChargeHandler{chargeService: cs}
}

func (h *ChargeHandler) CreateCharge(c *gin.Context) {
	var req models.ChargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idempotencyKey := c.GetHeader("X-Idempotency-Key")

	merchantIDContext, exists := c.Get("merchant_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized access"})
		return
	}
	merchantID := merchantIDContext.(string)

	trx, err := h.chargeService.ProcessCharge(&req, idempotencyKey, merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, models.ChargeResponse{
		Status:  "success",
		Message: "Charge routed and created successfully",
		Data:    *trx,
	})
}

func (h *ChargeHandler) GetChargeStatus(c *gin.Context) {
	transactionID := c.Param("id")

	trx, err := h.chargeService.GetTransaction(transactionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   trx,
	})
}

func (h *ChargeHandler) HandleWebhook(c *gin.Context) {
	gatewayName := c.Param("gateway_name")

	// FIX: MENGAMBIL HEADER KEAMANAN DARI PAYMENT GATEWAY
	// Xendit biasanya menggunakan "X-Callback-Token", dll. Kita ambil beberapa fallback standar.
	signature := c.GetHeader("X-Callback-Token")
	if signature == "" {
		signature = c.GetHeader("X-Signature") // Bisa disesuaikan lagi jika butuh standard lain
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Gagal membaca body payload"})
		return
	}

	payloadStr := string(bodyBytes)

	// FIX: Oper signature ke service untuk divalidasi
	err = h.chargeService.ProcessWebhook(gatewayName, payloadStr, signature)
	if err != nil {
		log.Printf("❌ Webhook Error [%s]: %v", gatewayName, err)
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()}) // 403 Forbidden
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}
