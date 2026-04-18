package http

import (
	"dupay/internal/models"
	"dupay/internal/service"
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

	// Ambil ID Merchant dari Context yang disisipkan oleh Middleware
	merchantIDContext, exists := c.Get("merchant_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized access"})
		return
	}
	merchantID := merchantIDContext.(string)

	// Teruskan merchantID ke Service
	trx, err := h.chargeService.ProcessCharge(&req, idempotencyKey, merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process charge: " + err.Error()})
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

func (h *ChargeHandler) MidtransWebhook(c *gin.Context) {
	var notification map[string]interface{}
	if err := c.ShouldBindJSON(&notification); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}

	orderID, ok1 := notification["order_id"].(string)
	transactionStatus, ok2 := notification["transaction_status"].(string)

	if !ok1 || !ok2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing required fields"})
		return
	}

	log.Printf("🔔 Webhook Midtrans! Order: %s, Status: %s", orderID, transactionStatus)

	internalStatus := "PENDING"
	switch transactionStatus {
	case "settlement", "capture":
		internalStatus = "SUCCESS"
	case "deny", "cancel", "expire":
		internalStatus = "FAILED"
	}

	err := h.chargeService.UpdateStatus(orderID, internalStatus)
	if err != nil {
		log.Printf("❌ Webhook Error: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}
