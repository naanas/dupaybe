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

// POST /v1/charges
func (h *ChargeHandler) CreateCharge(c *gin.Context) {
	var req models.ChargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Ambil X-Idempotency-Key dari header
	idempotencyKey := c.GetHeader("X-Idempotency-Key")

	// PENTING: Ambil merchant_id yang sudah divalidasi dan disisipkan oleh APISecurityMiddleware
	merchantIDContext, exists := c.Get("merchant_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Akses tidak sah: Merchant ID tidak ditemukan"})
		return
	}
	merchantID := merchantIDContext.(string)

	// Eksekusi proses charge dengan merchant_id yang dinamis
	trx, err := h.chargeService.ProcessCharge(&req, idempotencyKey, merchantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses charge: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, models.ChargeResponse{
		Status:  "success",
		Message: "Charge routed and created successfully",
		Data:    *trx,
	})
}

// GET /v1/charges/:id
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

// ----------------------------------------------------------------------
// UNIVERSAL WEBHOOK HANDLER
// ----------------------------------------------------------------------
// POST /v1/webhooks/:gateway_name
// Handler ini sekarang otomatis mendukung Midtrans, Xendit, atau PG lainnya
// selama konfigurasi 'webhook_mapping' di CMS sudah diisi dengan benar.
func (h *ChargeHandler) HandleWebhook(c *gin.Context) {
	// Ambil nama gateway dari URL (misal: "midtrans" atau "xendit")
	gatewayName := c.Param("gateway_name")

	// Baca seluruh body request dari Payment Gateway
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Gagal membaca body payload"})
		return
	}

	payloadStr := string(bodyBytes)
	log.Printf("🔔 Notifikasi Webhook diterima dari: %s", gatewayName)
	log.Printf("📦 Payload: %s", payloadStr)

	// Proses webhook secara dinamis di level service menggunakan mapping database
	err = h.chargeService.ProcessWebhook(gatewayName, payloadStr)
	if err != nil {
		log.Printf("❌ Gagal memproses webhook [%s]: %v", gatewayName, err)
		// Kirim 500 agar PG mencoba mengirim ulang (retry) jika memang ada error sistem
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Mayoritas Payment Gateway mewajibkan respon 200 OK untuk menghentikan pengiriman ulang
	c.JSON(http.StatusOK, gin.H{
		"status":  "received",
		"gateway": gatewayName,
	})
}
