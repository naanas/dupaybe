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
	UpdateStatus(orderID string, status string, gatewayID string) error               // FIX: Tambah GatewayID
	ProcessWebhook(gatewayName string, payload string, webhookSignature string) error // FIX: Tambah Signature
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

	// FIX: CEK IDEMPOTENCY KEY (Mencegah Double Charge jika klien koneksinya terputus lalu retry)
	var existingTrx models.Transaction
	if err := s.db.Where("idempotency_key = ? AND merchant_id = ?", idempotencyKey, merchantID).First(&existingTrx).Error; err == nil {
		// Jika transaksi sudah pernah dibuat dengan kunci ini, kembalikan transaksi lamanya (jangan potong saldo lagi)
		return &existingTrx, nil
	}

	var pg models.PaymentGateway
	if err := s.db.Where("name = ? AND is_active = ?", req.GatewayName, true).First(&pg).Error; err != nil {
		return nil, fmt.Errorf("payment gateway %s tidak ditemukan", req.GatewayName)
	}

	payloadStr := pg.RequestTemplate
	payloadStr = strings.ReplaceAll(payloadStr, "{{order_id}}", req.OrderID)
	payloadStr = strings.ReplaceAll(payloadStr, "{{amount}}", fmt.Sprintf("%.0f", req.Amount))
	payloadStr = strings.ReplaceAll(payloadStr, "{{payment_method}}", req.PaymentMethod)

	targetURL := pg.BaseURL + pg.ChargeEndpoint
	httpReq, err := http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
	if err != nil {
		return nil, errors.New("gagal merakit HTTP request")
	}

	httpReq.Header.Set("Content-Type", "application/json")

	decryptedServerKey := ""
	if pg.ServerKey != "" {
		key, err := crypto.DecryptAES([]byte(s.cfg.AppEncryptionKey), pg.ServerKey)
		if err != nil {
			return nil, errors.New("gagal mendekripsi kredensial gateway")
		}
		decryptedServerKey = key
	}

	switch pg.AuthType {
	case "BASIC_AUTH":
		httpReq.SetBasicAuth(decryptedServerKey, "")
	case "BEARER_TOKEN":
		httpReq.Header.Set("Authorization", "Bearer "+decryptedServerKey)
	case "CUSTOM_HEADER":
		httpReq.Header.Set(pg.CustomAuthHeader, decryptedServerKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi %s: %v", pg.Name, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	var responseMapping map[string]string
	json.Unmarshal([]byte(pg.ResponseMapping), &responseMapping)

	pgRefID := gjson.Get(respStr, responseMapping["pg_transaction_id"]).String()
	checkoutURL := gjson.Get(respStr, responseMapping["checkout_url"]).String()

	trx := &models.Transaction{
		ID:               uuid.New().String(),
		MerchantID:       merchantID,
		PaymentGatewayID: pg.ID,
		OrderID:          req.OrderID,
		IdempotencyKey:   idempotencyKey, // FIX: Simpan ke DB
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

// FIX: Update Status sekarang memfilter berdasarkan gatewayID agar tidak bentrok orderID beda merchant
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

// FIX: Menambah layer validasi keamanan webhook dari PG target
func (s *chargeService) ProcessWebhook(gatewayName string, payload string, webhookSignature string) error {
	var pg models.PaymentGateway
	if err := s.db.Where("LOWER(name) = LOWER(?) AND is_active = ?", gatewayName, true).First(&pg).Error; err != nil {
		return fmt.Errorf("gateway %s tidak ditemukan", gatewayName)
	}

	// FIX: VALIDASI KEAMANAN WEBHOOK
	if pg.WebhookValidationType == "TOKEN_MATCH" && pg.WebhookSecret != "" {
		// Validasi simple seperti Xendit (Token Verifikasi statis di header)
		if webhookSignature != pg.WebhookSecret {
			return errors.New("UNAUTHORIZED: Webhook signature tidak valid")
		}
	}

	var mapping map[string]interface{}
	if err := json.Unmarshal([]byte(pg.WebhookMapping), &mapping); err != nil {
		return errors.New("konfigurasi webhook_mapping di database tidak valid")
	}

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

	// FIX: Panggil UpdateStatus dengan PG ID agar tidak salah update transaksi milik Merchant lain
	return s.UpdateStatus(orderID, internalStatus, pg.ID)
}
