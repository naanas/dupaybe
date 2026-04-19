package service

import (
	"bytes"
	"dupay/internal/config"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

type ChargeService interface {
	ProcessCharge(req *models.ChargeRequest, idempotencyKey string, merchantID string) (*models.Transaction, error)
	GetTransaction(id string) (*models.Transaction, error)
	UpdateStatus(orderID string, status string, gatewayID string) error
	ProcessWebhook(gatewayName string, payload string, webhookSignature string) error
}

type chargeService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewChargeService(db *gorm.DB, cfg *config.Config) ChargeService {
	return &chargeService{db: db, cfg: cfg}
}

func (s *chargeService) ProcessCharge(req *models.ChargeRequest, idempotencyKey string, merchantID string) (*models.Transaction, error) {
	if idempotencyKey == "" {
		return nil, errors.New("idempotency key is missing")
	}

	var existingTrx models.Transaction
	if err := s.db.Where("idempotency_key = ? AND merchant_id = ?", idempotencyKey, merchantID).First(&existingTrx).Error; err == nil {
		return &existingTrx, nil
	}

	var pg models.PaymentGateway
	if err := s.db.Where("name = ? AND is_active = ?", req.GatewayName, true).First(&pg).Error; err != nil {
		return nil, fmt.Errorf("payment gateway %s tidak ditemukan", req.GatewayName)
	}

	// 1. Siapkan Template JSON Dasar
	payloadStr := pg.RequestTemplate
	payloadStr = strings.ReplaceAll(payloadStr, "{{order_id}}", req.OrderID)
	payloadStr = strings.ReplaceAll(payloadStr, "{{payment_method}}", req.PaymentMethod)
	payloadStr = strings.ReplaceAll(payloadStr, "\"{{amount}}\"", fmt.Sprintf("%.0f", req.Amount))

	// 2. Dekripsi Credentials
	decryptedServerKey := ""
	if pg.ServerKey != "" {
		key, _ := crypto.DecryptAES([]byte(s.cfg.AppEncryptionKey), pg.ServerKey)
		decryptedServerKey = key
	}

	decryptedPrivateKey := ""
	if pg.PrivateKey != "" {
		key, _ := crypto.DecryptAES([]byte(s.cfg.AppEncryptionKey), pg.PrivateKey)
		decryptedPrivateKey = key
	}

	// ==========================================
	// 3. ADAPTER TRIPAY (SUNTIK SIGNATURE KE BODY)
	// ==========================================
	if strings.Contains(strings.ToLower(pg.Name), "tripay") {
		// Rumus Tripay: MerchantCode + MerchantRef + Amount
		sigString := fmt.Sprintf("%s%s%.0f", pg.MerchantCode, req.OrderID, req.Amount)
		signature := crypto.GenerateHMAC256(sigString, decryptedPrivateKey)

		// FIX: Tripay minta signature di DALAM Payload JSON, bukan di Header!
		// Kita suntikkan manual ke string JSON sebelum merakit Request
		payloadStr = strings.TrimSpace(payloadStr)
		if strings.HasSuffix(payloadStr, "}") {
			payloadStr = payloadStr[:len(payloadStr)-1] + fmt.Sprintf(`, "signature": "%s"}`, signature)
		}
	}

	// 4. Rakit HTTP Request
	targetURL := pg.BaseURL + pg.ChargeEndpoint
	httpReq, err := http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
	if err != nil {
		return nil, errors.New("gagal merakit HTTP request")
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// 5. Set Autentikasi Standard (Bearer/Basic)
	switch pg.AuthType {
	case "BASIC_AUTH":
		httpReq.SetBasicAuth(decryptedServerKey, "")
	case "BEARER_TOKEN", "Bearer Auth":
		httpReq.Header.Set("Authorization", "Bearer "+decryptedServerKey)
	case "CUSTOM_HEADER":
		httpReq.Header.Set(pg.CustomAuthHeader, decryptedServerKey)
	}

	// 6. Eksekusi Tembakan ke PG Target
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi %s: %v", pg.Name, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	// TANGKAP ERROR JIKA PG MENOLAK REQUEST
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Ditolak oleh %s (Code: %d): %s", pg.Name, resp.StatusCode, respStr)
	}

	// 7. Mapping Response
	var responseMapping map[string]string
	if pg.ResponseMapping != "" && pg.ResponseMapping != "{}" {
		json.Unmarshal([]byte(pg.ResponseMapping), &responseMapping)
	}

	pgRefID := gjson.Get(respStr, responseMapping["pg_transaction_id"]).String()
	checkoutURL := gjson.Get(respStr, responseMapping["checkout_url"]).String()

	// Fallback khusus kalau response mapping belum disetting
	if pgRefID == "" {
		pgRefID = gjson.Get(respStr, "data.reference").String()
	}
	if checkoutURL == "" {
		checkoutURL = gjson.Get(respStr, "data.checkout_url").String()
	}

	// 8. Simpan ke Database
	trx := &models.Transaction{
		ID:               uuid.New().String(),
		MerchantID:       merchantID,
		PaymentGatewayID: pg.ID,
		OrderID:          req.OrderID,
		IdempotencyKey:   idempotencyKey,
		Amount:           req.Amount,
		PaymentMethod:    req.PaymentMethod,
		Status:           "PENDING",
		PGReferenceID:    pgRefID,
		CheckoutURL:      checkoutURL,
		CreatedAt:        time.Now(),
	}

	if err := s.db.Create(trx).Error; err != nil {
		return nil, err
	}

	return trx, nil
}

func (s *chargeService) GetTransaction(id string) (*models.Transaction, error) {
	var trx models.Transaction
	if err := s.db.Where("id = ?", id).First(&trx).Error; err != nil {
		return nil, errors.New("transaction not found")
	}
	return &trx, nil
}

func (s *chargeService) UpdateStatus(orderID string, status string, gatewayID string) error {
	result := s.db.Model(&models.Transaction{}).Where("order_id = ? AND payment_gateway_id = ?", orderID, gatewayID).Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("transaksi tidak ditemukan (order_id atau gateway salah)")
	}
	return nil
}

func (s *chargeService) ProcessWebhook(gatewayName string, payload string, webhookSignature string) error {
	var pg models.PaymentGateway
	if err := s.db.Where("LOWER(name) = LOWER(?) AND is_active = ?", gatewayName, true).First(&pg).Error; err != nil {
		return fmt.Errorf("gateway %s tidak ditemukan", gatewayName)
	}

	decryptedPrivateKey := ""
	if pg.PrivateKey != "" {
		key, _ := crypto.DecryptAES([]byte(s.cfg.AppEncryptionKey), pg.PrivateKey)
		decryptedPrivateKey = key
	}

	if strings.Contains(strings.ToLower(pg.Name), "tripay") {
		expectedSignature := crypto.GenerateHMAC256(payload, decryptedPrivateKey)
		if !strings.EqualFold(webhookSignature, expectedSignature) {
			return errors.New("UNAUTHORIZED: Webhook signature tidak valid")
		}
	} else if pg.WebhookValidationType == "TOKEN_MATCH" && pg.WebhookSecret != "" {
		if webhookSignature != pg.WebhookSecret {
			return errors.New("UNAUTHORIZED: Webhook token tidak valid")
		}
	}

	var mapping map[string]interface{}
	json.Unmarshal([]byte(pg.WebhookMapping), &mapping)

	orderIDPath, _ := mapping["order_id_path"].(string)
	statusPath, _ := mapping["status_path"].(string)

	orderID := gjson.Get(payload, orderIDPath).String()
	pgStatus := gjson.Get(payload, statusPath).String()

	if orderID == "" || pgStatus == "" {
		return errors.New("gagal mengekstrak data dari payload")
	}

	internalStatus := "PENDING"
	if successStatuses, ok := mapping["success_statuses"].([]interface{}); ok {
		for _, st := range successStatuses {
			if st.(string) == pgStatus {
				internalStatus = "SUCCESS"
				break
			}
		}
	}
	if failedStatuses, ok := mapping["failed_statuses"].([]interface{}); ok {
		for _, st := range failedStatuses {
			if st.(string) == pgStatus {
				internalStatus = "FAILED"
				break
			}
		}
	}
	if refundedStatuses, ok := mapping["refunded_statuses"].([]interface{}); ok {
		for _, st := range refundedStatuses {
			if st.(string) == pgStatus {
				internalStatus = "REFUNDED"
				break
			}
		}
	}

	// 1. Update Database Dupay
	err := s.UpdateStatus(orderID, internalStatus, pg.ID)
	if err != nil {
		return err
	}

	// 2. TRIGGER WEBHOOK FORWARDER SECARA ASYNC (BACKGROUND)
	// Kita pakai goroutine (kata kunci 'go') biar nggak bikin nunggu server Tripay-nya
	go s.forwardWebhookToMerchant(orderID, pg.ID)

	return nil
}

// --- TAMBAHKAN FUNGSI BARU INI DI PALING BAWAH FILE ---
func (s *chargeService) forwardWebhookToMerchant(orderID string, gatewayID string) {
	// 1. Cari data Transaksi Lengkap
	var trx models.Transaction
	if err := s.db.Where("order_id = ? AND payment_gateway_id = ?", orderID, gatewayID).First(&trx).Error; err != nil {
		return // Transaksi gak ketemu, batal forward
	}

	// 2. Cari data Merchant untuk ambil WebhookURL & SecretKey
	var merchant models.Merchant
	if err := s.db.Where("id = ?", trx.MerchantID).First(&merchant).Error; err != nil {
		return
	}

	if merchant.WebhookURL == "" {
		log.Printf("⚠️ [WEBHOOK FORWARDER] Merchant %s tidak punya Webhook URL, skip forwarding.", merchant.Name)
		return
	}

	// 3. Rakit Payload Bersih Ala Dupay (Standarisasi)
	payloadObj := map[string]interface{}{
		"order_id":       trx.OrderID,
		"amount":         trx.Amount,
		"status":         trx.Status,
		"payment_method": trx.PaymentMethod,
		"timestamp":      time.Now().Unix(),
	}
	payloadBytes, _ := json.Marshal(payloadObj)
	payloadStr := string(payloadBytes)

	// 4. Bikin Signature Dupay (Biar Klien tau ini asli dari kita, bukan hacker)
	// Kita hash pakai SecretKey si Merchant (sk_...)
	signature := crypto.GenerateHMAC256(payloadStr, merchant.SecretKey)

	// 5. Tembak ke Server Klien
	httpReq, _ := http.NewRequest("POST", merchant.WebhookURL, bytes.NewBuffer(payloadBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dupay-Signature", signature)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)

	if err != nil {
		log.Printf("❌ [WEBHOOK FORWARDER] Gagal nembak ke %s: %v", merchant.Name, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("✅ [WEBHOOK FORWARDER] Sukses kirim status %s ke %s (HTTP %d)", trx.Status, merchant.Name, resp.StatusCode)
}
