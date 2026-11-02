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
	SyncStatus(id string, merchantID string) (*models.Transaction, string, error)
	FetchPGChannels(draft models.PaymentGateway) ([]ChannelSyncItem, error)
	TestEndpoint(draft models.PaymentGateway, ep models.GatewayEndpoint, in EndpointTestInput) (*EndpointTestResult, error)
	GetPublicChannels(gatewayName string) ([]ChannelInfo, error)
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

	if s.disabledMethods()[strings.ToLower(strings.TrimSpace(req.PaymentMethod))] {
		return nil, fmt.Errorf("metode bayar %s nggak tersedia", req.PaymentMethod)
	}

	// Merchant cukup kirim metode bayar; gateway dipilih Dupay. gateway_name cuma buat maksa gateway tertentu.
	var pg models.PaymentGateway
	if req.GatewayName != "" {
		if err := s.db.Preload("Endpoints").Where("name = ? AND is_active = ?", req.GatewayName, true).First(&pg).Error; err != nil {
			return nil, fmt.Errorf("payment gateway %s tidak ditemukan", req.GatewayName)
		}
	} else {
		picked, err := s.pickGateway(req.PaymentMethod, req.Amount)
		if err != nil {
			return nil, err
		}
		pg = *picked
	}

	// Channel Mapping: translate kode internal client (mis. "bca", "qris") ke kode PG
	// (mis. "BCAVA", "QRIS"), sekaligus ambil jenis channel-nya (va/qris/...) buat milih endpoint.
	// Fallback: kalau lookup ga ketemu, pake raw value apa adanya.
	methodCode, channelMethod := findChannel(&pg, req.PaymentMethod)

	ep := resolveEndpoint(&pg, models.EndpointActionCharge, channelMethod)
	if ep == nil {
		return nil, fmt.Errorf("gateway %s belum punya endpoint charge yang aktif", pg.Name)
	}

	res, err := s.callGateway(&pg, ep, pgCallVars{
		OrderID:       req.OrderID,
		Amount:        req.Amount,
		PaymentMethod: methodCode,
		MerchantCode:  pg.MerchantCode,
	})
	if err != nil {
		return nil, err
	}
	statusCode, respStr := res.StatusCode, res.Body

	clientPayloadBytes, _ := json.Marshal(req)
	clientPayload := string(clientPayloadBytes)

	if statusCode >= 400 {
		lowerResp := strings.ToLower(respStr)
		if strings.Contains(lowerResp, "whitelist") || (strings.Contains(lowerResp, "ip") && strings.Contains(lowerResp, "allow")) {
			return nil, fmt.Errorf("Ditolak oleh %s (Code: %d): indikasi IP belum di-whitelist di Payment Gateway. Tambahkan IP server Dupay ke whitelist di dashboard PG. Detail: %s", pg.Name, statusCode, respStr)
		}
		return nil, fmt.Errorf("Ditolak oleh %s (Code: %d): %s", pg.Name, statusCode, respStr)
	}

	var responseMapping map[string]string
	if ep.ResponseMapping != "" && ep.ResponseMapping != "{}" {
		json.Unmarshal([]byte(ep.ResponseMapping), &responseMapping)
	}

	extract := func(paths string) string { return extractPaths(respStr, paths) }

	pgRefID := extract(responseMapping["pg_transaction_id"])
	checkoutURL := extract(responseMapping["checkout_url"])
	paymentCode := extract(responseMapping["payment_code"])
	paymentDeeplink := extract(responseMapping["payment_deeplink"])

	// Defensive fallback: kalau response_mapping belum diset benar di CMS,
	// tetap coba ambil field umum dari provider (Tripay, dll) sebelum fallback ke checkout URL.
	if paymentCode == "" {
		paymentCode = extract("data.pay_code|data.qr_string|data.qr_url|pay_code|qr_string|qr_url")
	}
	if checkoutURL == "" {
		checkoutURL = extract("data.checkout_url|checkout_url|data.payment_url|payment_url")
	}
	if paymentDeeplink == "" {
		paymentDeeplink = extract("data.deeplink_url|data.deeplink|data.deep_link|deeplink_url|deeplink|deep_link")
	}

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
		PaymentCode:      paymentCode,
		PaymentDeeplink:  paymentDeeplink,
		ClientPayload:    clientPayload,
		PGResponse:       respStr,
		PGStatusCode:     statusCode,
		CreatedAt:        time.Now(),
	}

	if err := s.db.Create(trx).Error; err != nil {
		return nil, err
	}

	return trx, nil
}

// GetPublicChannels mengembalikan daftar channel AKTIF buat sebuah gateway.
// Ini di-expose lewat endpoint public (tanpa auth) supaya frontend bisa render
// list metode pembayaran dinamis tanpa hardcoded.
//
// Field sensitif (PGCode) dihilangkan sebelum di-return, supaya kode internal PG
// nggak bocor ke publik. Frontend cukup pake `code` yang dia kirim balik saat charge,
// dan orchestrator yang terjemahin ke PGCode lewat channel_mapping.
func (s *chargeService) GetPublicChannels(gatewayName string) ([]ChannelInfo, error) {
	var active []ChannelInfo
	if gatewayName != "" {
		var pg models.PaymentGateway
		if err := s.db.Where("is_active = ? AND LOWER(name) = LOWER(?)", true, gatewayName).First(&pg).Error; err != nil {
			return nil, fmt.Errorf("gateway tidak ditemukan atau tidak aktif")
		}
		all, _ := ParseChannels(pg.ChannelMapping)
		active = ActiveChannels(all)
	} else {
		// Tanpa gateway: gabungan semua gateway aktif. Kode yang sama diambil dari gateway yang
		// bakal dipakai waktu charge (prioritas paling tinggi), biar fee/min/max yang tampil sesuai.
		gateways, err := s.activeGatewaysByPriority()
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, pg := range gateways {
			all, _ := ParseChannels(pg.ChannelMapping)
			for _, ch := range ActiveChannels(all) {
				key := strings.ToLower(strings.TrimSpace(ch.Code))
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				active = append(active, ch)
			}
		}
	}

	// Metode bayar yang dimatiin admin nggak ditampilin ke merchant.
	disabled := s.disabledMethods()
	enabled := active[:0]
	for _, ch := range active {
		if !disabled[strings.ToLower(strings.TrimSpace(ch.Code))] {
			enabled = append(enabled, ch)
		}
	}
	active = enabled

	// Sanitasi: hilangkan pg_code dari response publik.
	out := make([]ChannelInfo, 0, len(active))
	for _, ch := range active {
		ch.PGCode = ""
		out = append(out, ch)
	}
	return out, nil
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

	confirmViaStatusAPI := false
	switch pg.AuthType {
	case "TRIPAY_HMAC":
		expectedSignature := crypto.GenerateHMAC256(payload, decryptedPrivateKey)
		if !strings.EqualFold(webhookSignature, expectedSignature) {
			return errors.New("UNAUTHORIZED: Webhook signature tidak valid")
		}
	case "IPAYMU_V2":
		// Signature nggak cocok / nggak ada: isi callback nggak dipercaya, status ditanya langsung ke iPaymu di bawah.
		confirmViaStatusAPI = !verifyIpaymuSignature(payload, webhookSignature, pg.MerchantCode)
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

	if confirmViaStatusAPI {
		var trx models.Transaction
		if err := s.db.Where("order_id = ? AND payment_gateway_id = ?", orderID, pg.ID).First(&trx).Error; err != nil {
			return errors.New("transaksi tidak ditemukan (order_id atau gateway salah)")
		}
		log.Printf("[WEBHOOK] signature %s nggak cocok, cek status order_id=%s langsung ke PG", pg.Name, orderID)
		if _, _, err := s.SyncStatus(trx.ID, ""); err != nil {
			return fmt.Errorf("UNAUTHORIZED: signature callback tidak valid dan cek status ke PG gagal: %v", err)
		}
		return nil
	}

	internalStatus := mapStatus(mapping, pgStatus)

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
		log.Printf("[WEBHOOK-FWD] transaksi tidak ditemukan for order_id=%s gateway_id=%s: %v", orderID, gatewayID, err)
		return
	}

	var merchant models.Merchant
	if err := s.db.Where("id = ?", trx.MerchantID).First(&merchant).Error; err != nil {
		log.Printf("[WEBHOOK-FWD] merchant tidak ditemukan id=%s: %v", trx.MerchantID, err)
		return
	}
	if merchant.WebhookURL == "" {
		log.Printf("[WEBHOOK-FWD] skip order_id=%s karena webhook_url merchant kosong", trx.OrderID)
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
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[WEBHOOK-FWD] gagal kirim ke merchant url=%s order_id=%s: %v", merchant.WebhookURL, trx.OrderID, err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[WEBHOOK-FWD] merchant callback non-2xx status=%d order_id=%s body=%s", resp.StatusCode, trx.OrderID, string(respBody))
		return
	}

	log.Printf("[WEBHOOK-FWD] sukses kirim callback ke merchant order_id=%s status=%s", trx.OrderID, trx.Status)
}

// SyncStatus nanya status transaksi langsung ke PG lewat endpoint "status" gateway-nya,
// buat jaga-jaga kalau webhook PG nggak nyampe. merchantID kosong = dipanggil dari CMS (boleh semua transaksi).
// Return: transaksi terbaru + status mentah dari PG.
func (s *chargeService) SyncStatus(id string, merchantID string) (*models.Transaction, string, error) {
	var trx models.Transaction
	if err := s.db.Where("id = ?", id).First(&trx).Error; err != nil {
		return nil, "", errors.New("transaction not found")
	}
	if merchantID != "" && trx.MerchantID != merchantID {
		return nil, "", errors.New("transaction not found")
	}

	var pg models.PaymentGateway
	if err := s.db.Preload("Endpoints").Where("id = ?", trx.PaymentGatewayID).First(&pg).Error; err != nil {
		return nil, "", errors.New("gateway transaksi ini tidak ditemukan")
	}

	methodCode, channelMethod := findChannel(&pg, trx.PaymentMethod)
	ep := resolveEndpoint(&pg, models.EndpointActionStatus, channelMethod)
	if ep == nil {
		return nil, "", fmt.Errorf("gateway %s belum punya endpoint status yang aktif", pg.Name)
	}

	res, err := s.callGateway(&pg, ep, pgCallVars{
		OrderID:       trx.OrderID,
		Amount:        trx.Amount,
		PaymentMethod: methodCode,
		PGReferenceID: trx.PGReferenceID,
		MerchantCode:  pg.MerchantCode,
	})
	if err != nil {
		return nil, "", err
	}
	statusCode, respStr := res.StatusCode, res.Body
	if statusCode >= 400 {
		return nil, "", fmt.Errorf("Ditolak oleh %s (Code: %d): %s", pg.Name, statusCode, respStr)
	}

	var responseMapping map[string]string
	json.Unmarshal([]byte(ep.ResponseMapping), &responseMapping)
	pgStatus := extractPaths(respStr, responseMapping["status"])
	if pgStatus == "" {
		return nil, "", fmt.Errorf("status tidak ditemukan di response %s, cek response_mapping.status di endpoint status. Response: %s", pg.Name, respStr)
	}

	var webhookMapping map[string]interface{}
	json.Unmarshal([]byte(pg.WebhookMapping), &webhookMapping)
	newStatus := mapStatus(webhookMapping, pgStatus)

	// PENDING = status PG belum final / nggak dikenal, jangan nimpa status yang udah ada.
	if newStatus != "PENDING" && newStatus != trx.Status {
		if err := s.db.Model(&models.Transaction{}).Where("id = ?", trx.ID).Update("status", newStatus).Error; err != nil {
			return nil, "", err
		}
		trx.Status = newStatus
		go s.forwardWebhookToMerchant(trx.OrderID, pg.ID)
	}

	return &trx, pgStatus, nil
}
