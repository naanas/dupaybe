package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"dupay/internal/config"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	payloadStr := pg.RequestTemplate
	payloadStr = strings.ReplaceAll(payloadStr, "{{order_id}}", req.OrderID)
	payloadStr = strings.ReplaceAll(payloadStr, "{{payment_method}}", strings.ToLower(req.PaymentMethod))
	payloadStr = strings.ReplaceAll(payloadStr, "\"{{amount}}\"", fmt.Sprintf("%.0f", req.Amount))

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

	targetURL := strings.TrimSuffix(pg.BaseURL, "/") + "/" + strings.TrimPrefix(pg.ChargeEndpoint, "/")
	httpReq, err := http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
	if err != nil {
		return nil, errors.New("gagal merakit HTTP request")
	}
	httpReq.Header.Set("Content-Type", "application/json")

	switch pg.AuthType {
	case "TRIPAY_HMAC":
		sigString := fmt.Sprintf("%s%s%.0f", pg.MerchantCode, req.OrderID, req.Amount)
		signature := crypto.GenerateHMAC256(sigString, decryptedPrivateKey)

		payloadStr = strings.TrimSpace(payloadStr)
		if strings.HasSuffix(payloadStr, "}") {
			payloadStr = payloadStr[:len(payloadStr)-1] + fmt.Sprintf(`, "signature": "%s"}`, signature)
		}
		httpReq, _ = http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
		httpReq.Header.Set("Content-Type", "application/json")

	case "IPAYMU_V2":
		buffer := new(bytes.Buffer)
		if err := json.Compact(buffer, []byte(payloadStr)); err == nil {
			payloadStr = buffer.String()
		}

		bodyHasher := sha256.New()
		bodyHasher.Write([]byte(payloadStr))
		bodyHashStr := hex.EncodeToString(bodyHasher.Sum(nil))

		stringToSign := fmt.Sprintf("POST:%s:%s:%s", pg.MerchantCode, bodyHashStr, decryptedPrivateKey)

		mac := hmac.New(sha256.New, []byte(decryptedPrivateKey))
		mac.Write([]byte(stringToSign))
		signatureHex := hex.EncodeToString(mac.Sum(nil))

		timestamp := time.Now().Format("20060102150405")

		httpReq, _ = http.NewRequest("POST", targetURL, bytes.NewBuffer([]byte(payloadStr)))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("va", pg.MerchantCode)
		httpReq.Header.Set("signature", signatureHex)
		httpReq.Header.Set("timestamp", timestamp)

	case "BASIC_AUTH":
		httpReq.SetBasicAuth(decryptedServerKey, "")
	case "BEARER_TOKEN", "Bearer Auth":
		httpReq.Header.Set("Authorization", "Bearer "+decryptedServerKey)
	case "CUSTOM_HEADER":
		if pg.CustomAuthHeader != "" {
			httpReq.Header.Set(pg.CustomAuthHeader, decryptedServerKey)
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi PG: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStr := string(respBody)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Ditolak oleh %s (Code: %d): %s", pg.Name, resp.StatusCode, respStr)
	}

	var responseMapping map[string]string
	if pg.ResponseMapping != "" && pg.ResponseMapping != "{}" {
		json.Unmarshal([]byte(pg.ResponseMapping), &responseMapping)
	}

	pgRefID := gjson.Get(respStr, responseMapping["pg_transaction_id"]).String()
	checkoutURL := gjson.Get(respStr, responseMapping["checkout_url"]).String()

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

	switch pg.AuthType {
	case "TRIPAY_HMAC":
		expectedSignature := crypto.GenerateHMAC256(payload, decryptedPrivateKey)
		if !strings.EqualFold(webhookSignature, expectedSignature) {
			return errors.New("UNAUTHORIZED: Webhook signature tidak valid")
		}
	case "IPAYMU_V2":
		// Bypass verifikasi (iPaymu sering nggak ngasih signature webhook)
	default:
		if pg.WebhookValidationType == "TOKEN_MATCH" && pg.WebhookSecret != "" {
			if webhookSignature != pg.WebhookSecret {
				return errors.New("UNAUTHORIZED: Webhook token tidak valid")
			}
		}
	}

	var mapping map[string]interface{}
	json.Unmarshal([]byte(pg.WebhookMapping), &mapping)

	orderIDPath, _ := mapping["order_id_path"].(string)
	statusPath, _ := mapping["status_path"].(string)

	// 1. Coba baca secara JSON
	orderID := gjson.Get(payload, orderIDPath).String()
	pgStatus := gjson.Get(payload, statusPath).String()

	// 2. FALLBACK: Kalau bukan JSON (kosong), kita parse gaya form-urlencoded
	if orderID == "" || pgStatus == "" {
		parsedForm, err := url.ParseQuery(payload)
		if err == nil {
			// Ambil berdasarkan mapping CMS
			orderID = parsedForm.Get(orderIDPath)
			pgStatus = parsedForm.Get(statusPath)

			// Hard-fallback khusus iPaymu (jaga-jaga mapping lu salah)
			if orderID == "" && pg.AuthType == "IPAYMU_V2" {
				orderID = parsedForm.Get("reference_id")
				if orderID == "" {
					orderID = parsedForm.Get("sid")
				}
				pgStatus = parsedForm.Get("status")
			}
		}
	}

	if orderID == "" || pgStatus == "" {
		return errors.New("gagal mengekstrak data dari payload")
	}

	internalStatus := "PENDING"
	if successStatuses, ok := mapping["success_statuses"].([]interface{}); ok {
		for _, st := range successStatuses {
			if strings.EqualFold(st.(string), pgStatus) {
				internalStatus = "SUCCESS"
				break
			}
		}
	}
	if failedStatuses, ok := mapping["failed_statuses"].([]interface{}); ok {
		for _, st := range failedStatuses {
			if strings.EqualFold(st.(string), pgStatus) {
				internalStatus = "FAILED"
				break
			}
		}
	}
	if refundedStatuses, ok := mapping["refunded_statuses"].([]interface{}); ok {
		for _, st := range refundedStatuses {
			if strings.EqualFold(st.(string), pgStatus) {
				internalStatus = "REFUNDED"
				break
			}
		}
	}

	err := s.UpdateStatus(orderID, internalStatus, pg.ID)
	if err != nil {
		return err
	}

	go s.forwardWebhookToMerchant(orderID, pg.ID)

	return nil
}

func (s *chargeService) forwardWebhookToMerchant(orderID string, gatewayID string) {
	var trx models.Transaction
	if err := s.db.Where("order_id = ? AND payment_gateway_id = ?", orderID, gatewayID).First(&trx).Error; err != nil {
		return
	}

	var merchant models.Merchant
	if err := s.db.Where("id = ?", trx.MerchantID).First(&merchant).Error; err != nil {
		return
	}
	if merchant.WebhookURL == "" {
		return
	}

	payloadObj := map[string]interface{}{
		"order_id":       trx.OrderID,
		"amount":         trx.Amount,
		"status":         trx.Status,
		"payment_method": trx.PaymentMethod,
		"timestamp":      time.Now().Unix(),
	}
	payloadBytes, _ := json.Marshal(payloadObj)

	signature := crypto.GenerateHMAC256(string(payloadBytes), merchant.SecretKey)

	httpReq, _ := http.NewRequest("POST", merchant.WebhookURL, bytes.NewBuffer(payloadBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dupay-Signature", signature)

	client := &http.Client{Timeout: 5 * time.Second}
	client.Do(httpReq)
}
