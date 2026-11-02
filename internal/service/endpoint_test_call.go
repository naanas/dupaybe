package service

import (
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/json"
	"errors"
	"strings"
)

// EndpointTestInput = nilai contoh buat placeholder waktu nge-test endpoint dari CMS.
type EndpointTestInput struct {
	OrderID       string  `json:"order_id"`
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"` // kode internal (mis. "bca"), diterjemahin lewat channel_mapping
	PGReferenceID string  `json:"pg_reference_id"`
}

type EndpointTestResult struct {
	PGCallResult
	// Mapped = hasil response mapping terhadap response PG, biar admin bisa ngecek mapping-nya bener.
	Mapped interface{} `json:"mapped,omitempty"`
}

// resolveDraftGateway nyusun gateway dari isi form CMS yang belum tentu disimpan.
// Kredensial di draft = plaintext (baru diketik); kalau kosong, pakai yang tersimpan (draft.ID).
func (s *chargeService) resolveDraftGateway(draft models.PaymentGateway) (*models.PaymentGateway, error) {
	pg := models.PaymentGateway{}
	if draft.ID != "" {
		if err := s.db.Where("id = ?", draft.ID).First(&pg).Error; err != nil {
			return nil, errors.New("gateway tidak ditemukan")
		}
	}

	// Jangan sampai kredensial tersimpan kekirim ke URL lain cuma karena base URL di form diganti.
	usesSavedKey := (draft.ServerKey == "" && pg.ServerKey != "") || (draft.PrivateKey == "" && pg.PrivateKey != "")
	if pg.ID != "" && usesSavedKey && strings.TrimSpace(draft.BaseURL) != strings.TrimSpace(pg.BaseURL) {
		return nil, errors.New("Base URL di form beda dari yang tersimpan. Isi ulang API Key / Private Key dulu buat test ke URL baru")
	}

	pg.Name = draft.Name
	pg.BaseURL = draft.BaseURL
	pg.AuthType = draft.AuthType
	pg.CustomAuthHeader = draft.CustomAuthHeader
	pg.MerchantCode = draft.MerchantCode
	pg.ChannelMapping = draft.ChannelMapping
	pg.Endpoints = draft.Endpoints

	key := []byte(s.cfg.AppEncryptionKey)
	if draft.ServerKey != "" {
		enc, err := crypto.EncryptAES(key, draft.ServerKey)
		if err != nil {
			return nil, err
		}
		pg.ServerKey = enc
	}
	if draft.PrivateKey != "" {
		enc, err := crypto.EncryptAES(key, draft.PrivateKey)
		if err != nil {
			return nil, err
		}
		pg.PrivateKey = enc
	}
	return &pg, nil
}

// TestEndpoint nembak satu endpoint pakai gateway dari isi form (lihat resolveDraftGateway).
// Transaksi nggak dibuat di Dupay & status nggak diubah, cuma ngembaliin request/response mentah.
func (s *chargeService) TestEndpoint(draft models.PaymentGateway, ep models.GatewayEndpoint, in EndpointTestInput) (*EndpointTestResult, error) {
	pg, err := s.resolveDraftGateway(draft)
	if err != nil {
		return nil, err
	}

	pgCode, _ := findChannel(pg, in.PaymentMethod)
	res, err := s.callGateway(pg, &ep, pgCallVars{
		OrderID:       in.OrderID,
		Amount:        in.Amount,
		PaymentMethod: pgCode,
		PGReferenceID: in.PGReferenceID,
		MerchantCode:  pg.MerchantCode,
	})
	if err != nil {
		return nil, err
	}

	out := &EndpointTestResult{PGCallResult: *res}
	var mapping map[string]string
	if json.Unmarshal([]byte(ep.ResponseMapping), &mapping) == nil && len(mapping) > 0 {
		if ep.Action == models.EndpointActionChannels {
			channels, err := parseChannelList(res.Body, mapping)
			if err != nil {
				out.Mapped = map[string]string{"error": err.Error()}
			} else {
				out.Mapped = channels
			}
		} else {
			mapped := map[string]string{}
			for key, paths := range mapping {
				mapped[key] = extractPaths(res.Body, paths)
			}
			out.Mapped = mapped
		}
	}
	return out, nil
}
