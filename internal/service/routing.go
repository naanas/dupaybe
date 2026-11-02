package service

import (
	"dupay/internal/models"
	"errors"
	"fmt"
	"strings"
)

func (s *chargeService) activeGatewaysByPriority() ([]models.PaymentGateway, error) {
	var gateways []models.PaymentGateway
	if err := s.db.Preload("Endpoints").Where("is_active = ?", true).Order("priority, name").Find(&gateways).Error; err != nil {
		return nil, errors.New("gagal membaca daftar gateway")
	}
	return gateways, nil
}

// pickGateway milih gateway buat sebuah metode bayar (kode merchant, mis. "bri") waktu merchant
// nggak nyebut gateway_name: gateway aktif dengan prioritas paling tinggi (angka paling kecil) yang
// punya channel aktif dengan kode itu, nominalnya masuk batas min/max channel, dan punya endpoint charge.
func (s *chargeService) pickGateway(paymentMethod string, amount float64) (*models.PaymentGateway, error) {
	gateways, err := s.activeGatewaysByPriority()
	if err != nil {
		return nil, err
	}
	return pickFrom(gateways, paymentMethod, amount)
}

// pickFrom = aturan pemilihan pickGateway; gateways harus udah urut prioritas.
func pickFrom(gateways []models.PaymentGateway, paymentMethod string, amount float64) (*models.PaymentGateway, error) {
	code := strings.ToLower(strings.TrimSpace(paymentMethod))
	var reasons []string
	for i := range gateways {
		pg := &gateways[i]
		all, _ := ParseChannels(pg.ChannelMapping)
		for _, ch := range all {
			if !ch.Active || strings.ToLower(strings.TrimSpace(ch.Code)) != code {
				continue
			}
			switch {
			case ch.MinAmount > 0 && amount < float64(ch.MinAmount):
				reasons = append(reasons, fmt.Sprintf("minimal %d", ch.MinAmount))
			case ch.MaxAmount > 0 && amount > float64(ch.MaxAmount):
				reasons = append(reasons, fmt.Sprintf("maksimal %d", ch.MaxAmount))
			case resolveEndpoint(pg, models.EndpointActionCharge, ch.Method) == nil:
				// gateway ini belum bisa charge, coba gateway berikutnya
			default:
				return pg, nil
			}
			break
		}
	}

	if len(reasons) > 0 {
		return nil, fmt.Errorf("nominal %.0f nggak bisa pakai metode bayar %s (%s)", amount, paymentMethod, strings.Join(reasons, ", "))
	}
	return nil, fmt.Errorf("metode bayar %s nggak tersedia", paymentMethod)
}

// disabledMethods = kode metode bayar yang dimatiin admin (huruf kecil).
func (s *chargeService) disabledMethods() map[string]bool {
	var codes []string
	s.db.Model(&models.DisabledPaymentMethod{}).Pluck("code", &codes)
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[strings.ToLower(strings.TrimSpace(c))] = true
	}
	return out
}
