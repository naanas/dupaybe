package service

import (
	"bytes"
	"dupay/internal/models"
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

// Interface sudah dilengkapi
type ChargeService interface {
	ProcessCharge(req *models.ChargeRequest, idempotencyKey string) (*models.Transaction, error)
	GetTransaction(id string) (*models.Transaction, error)
	UpdateStatus(orderID string, status string) error
}

type chargeService struct {
	db *gorm.DB
}

func NewChargeService(db *gorm.DB) ChargeService {
	return &chargeService{db: db}
}

func (s *chargeService) ProcessCharge(req *models.ChargeRequest, idempotencyKey string) (*models.Transaction, error) {
	if idempotencyKey == "" {
		return nil, errors.New("idempotency key is missing")
	}

	// 1. Ambil Kontrak API Gateway dari Database berdasarkan nama (Misal: "MIDTRANS")
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

	// 4. Set Autentikasi secara dinamis
	dummyServerKey := "ServerKey-123"
	switch pg.AuthType {
	case "BASIC_AUTH":
		httpReq.SetBasicAuth(dummyServerKey, "")
	case "BEARER_TOKEN":
		httpReq.Header.Set("Authorization", "Bearer "+dummyServerKey)
	case "CUSTOM_HEADER":
		httpReq.Header.Set(pg.CustomAuthHeader, dummyServerKey)
	}

	// 5. EKSEKUSI REQUEST KE PAYMENT GATEWAY LUAR
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi %s: %v", pg.Name, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	// 6. JSONPath MAPPING: Ekstrak URL dan ID dari response
	var responseMapping map[string]string
	json.Unmarshal([]byte(pg.ResponseMapping), &responseMapping)

	pgRefID := gjson.Get(respStr, responseMapping["pg_transaction_id"]).String()
	checkoutURL := gjson.Get(respStr, responseMapping["checkout_url"]).String()

	// 7. Simpan Transaksi ke Database Dupay
	trx := &models.Transaction{
		ID:               uuid.New().String(),
		MerchantID:       "merchant-123",
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

// Fungsi tambahan yang kurang sebelumnya
func (s *chargeService) GetTransaction(id string) (*models.Transaction, error) {
	var trx models.Transaction
	if err := s.db.Where("id = ?", id).First(&trx).Error; err != nil {
		return nil, errors.New("transaction not found")
	}
	return &trx, nil
}

// Fungsi tambahan yang kurang sebelumnya
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
