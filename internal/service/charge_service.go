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

// Interface sudah dilengkapi dengan ProcessWebhook
type ChargeService interface {
	ProcessCharge(req *models.ChargeRequest, idempotencyKey string, merchantID string) (*models.Transaction, error)
	GetTransaction(id string) (*models.Transaction, error)
	UpdateStatus(orderID string, status string) error
	ProcessWebhook(gatewayName string, payload string) error
}

type chargeService struct {
	db  *gorm.DB
	cfg *config.Config
}

// Constructor sekarang menerima Config juga untuk keperluan dekripsi AES
func NewChargeService(db *gorm.DB, cfg *config.Config) ChargeService {
	return &chargeService{db: db, cfg: cfg}
}

func (s *chargeService) ProcessCharge(req *models.ChargeRequest, idempotencyKey string, merchantID string) (*models.Transaction, error) {
	if idempotencyKey == "" {
		return nil, errors.New("idempotency key is missing")
	}

	// 1. Ambil Kontrak API Gateway dari Database berdasarkan nama
	var pg models.PaymentGateway
	if err := s.db.Where("name = ? AND is_active = ?", req.GatewayName, true).First(&pg).Error; err != nil {
		return nil, fmt.Errorf("payment gateway %s tidak ditemukan atau tidak aktif", req.GatewayName)
	}

	// 2. TEMPLATE ENGINE: Merakit Request JSON secara dinamis
	payloadStr := pg.RequestTemplate
	payloadStr = strings.ReplaceAll(payloadStr, "{{order_id}}", req.OrderID)
	payloadStr = strings.ReplaceAll(payloadStr, "{{amount}}", fmt.Sprintf("%.0f", req.Amount))
	payloadStr = strings.ReplaceAll(payloadStr, "{{payment_method}}", req.PaymentMethod)

	// 3. Siapkan HTTP Request ke Server PG Target
	targetURL := pg.BaseURL + pg.ChargeEndpoint
	httpReq, err := http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
	if err != nil {
		return nil, errors.New("gagal merakit HTTP request")
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// 4. DEKRIPSI SERVER KEY DARI DATABASE SECARA DINAMIS (AES-256)
	decryptedServerKey := ""
	if pg.ServerKey != "" {
		key, err := crypto.DecryptAES([]byte(s.cfg.AppEncryptionKey), pg.ServerKey)
		if err != nil {
			return nil, errors.New("gagal mendekripsi kredensial gateway, periksa APP_ENCRYPTION_KEY di .env")
		}
		decryptedServerKey = key
	}

	// 5. Set Autentikasi secara dinamis menggunakan kunci yang sudah didekripsi
	switch pg.AuthType {
	case "BASIC_AUTH":
		httpReq.SetBasicAuth(decryptedServerKey, "")
	case "BEARER_TOKEN":
		httpReq.Header.Set("Authorization", "Bearer "+decryptedServerKey)
	case "CUSTOM_HEADER":
		httpReq.Header.Set(pg.CustomAuthHeader, decryptedServerKey)
	}

	// 6. EKSEKUSI REQUEST KE PAYMENT GATEWAY LUAR
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi %s: %v", pg.Name, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	// 7. JSONPath MAPPING: Ekstrak URL dan ID dari response dinamis
	var responseMapping map[string]string
	if err := json.Unmarshal([]byte(pg.ResponseMapping), &responseMapping); err != nil {
		return nil, errors.New("konfigurasi response_mapping di database tidak valid")
	}

	pgRefID := gjson.Get(respStr, responseMapping["pg_transaction_id"]).String()
	checkoutURL := gjson.Get(respStr, responseMapping["checkout_url"]).String()

	// 8. Simpan Transaksi ke Database Dupay menggunakan merchant_id asli dari middleware
	trx := &models.Transaction{
		ID:               uuid.New().String(),
		MerchantID:       merchantID, // Sudah dinamis!
		PaymentGatewayID: pg.ID,
		OrderID:          req.OrderID,
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

func (s *chargeService) UpdateStatus(orderID string, status string) error {
	result := s.db.Model(&models.Transaction{}).Where("order_id = ?", orderID).Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("order_id not found")
	}
	return nil
}

// ----------------------------------------------------------------------
// NEW: Fungsi pemrosesan Webhook yang Universal / Parameterized
// ----------------------------------------------------------------------

func (s *chargeService) ProcessWebhook(gatewayName string, payload string) error {
	// 1. Ambil config Gateway berdasarkan nama dari URL webhook (misal: "midtrans" / "xendit")
	var pg models.PaymentGateway
	// LOWER() agar tidak case-sensitive jika dari parameter URL
	if err := s.db.Where("LOWER(name) = LOWER(?) AND is_active = ?", gatewayName, true).First(&pg).Error; err != nil {
		return fmt.Errorf("gateway %s tidak ditemukan atau tidak aktif", gatewayName)
	}

	// 2. Parse WebhookMapping dari Database
	var mapping map[string]interface{}
	if err := json.Unmarshal([]byte(pg.WebhookMapping), &mapping); err != nil {
		return errors.New("konfigurasi webhook_mapping di database tidak valid")
	}

	orderIDPath, _ := mapping["order_id_path"].(string)
	statusPath, _ := mapping["status_path"].(string)

	// 3. Ekstrak nilai secara dinamis menggunakan gjson berdasarkan path yang diset di CMS
	orderID := gjson.Get(payload, orderIDPath).String()
	pgStatus := gjson.Get(payload, statusPath).String()

	if orderID == "" || pgStatus == "" {
		return errors.New("gagal mengekstrak order_id atau status dari payload webhook")
	}

	// 4. Translasi Status Gateway Luar -> menjadi Status Internal Dupay
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

	// 5. Update status di tabel transaksi
	return s.UpdateStatus(orderID, internalStatus)
}
