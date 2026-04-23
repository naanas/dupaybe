package service

import (
	"encoding/json"
	"strings"
)

// ChannelInfo adalah representasi satu payment channel di gateway.
// Disimpan sebagai JSON array di kolom payment_gateways.channel_mapping.
//
// Skema baru (array of object) = source of truth. Kalau data lama masih
// format key-value object ({"bca": "BCAVA"}), ParseChannels tetap bisa baca
// via fallback dan "promote" jadi ChannelInfo minimal (tanpa metadata).
type ChannelInfo struct {
	Code       string  `json:"code"`                  // kode internal dari client (mis. "bca", "qris")
	PGCode     string  `json:"pg_code"`               // kode yang dikirim ke PG (mis. "BCAVA")
	Label      string  `json:"label,omitempty"`       // display name di UI
	Method     string  `json:"method,omitempty"`      // va|qris|cstore|ewallet
	Group      string  `json:"group,omitempty"`       // "Virtual Account" | "QRIS" | "E-Wallet" | "Retail"
	Logo       string  `json:"logo,omitempty"`        // URL logo (opsional)
	MinAmount  int64   `json:"min_amount,omitempty"`  // Rupiah
	MaxAmount  int64   `json:"max_amount,omitempty"`  // Rupiah
	FeeFlat    int64   `json:"fee_flat,omitempty"`    // biaya flat Rupiah
	FeePercent float64 `json:"fee_percent,omitempty"` // biaya persen (0.7 = 0.7%)
	Active     bool    `json:"active"`
}

// ParseChannels membaca string JSON dari kolom channel_mapping dan mengembalikan:
//   - channels: daftar ChannelInfo (untuk endpoint publik GET /channels)
//   - lookup:   map code(lowercase) -> pg_code (untuk translate saat charge)
//
// Support 2 format biar backward-compatible:
//  1. Array format (baru): [{"code":"bca","pg_code":"BCAVA",...}, ...]
//  2. Object format (lama): {"bca":"BCAVA","qris":"QRIS",...}
func ParseChannels(raw string) ([]ChannelInfo, map[string]string) {
	lookup := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "[]" || raw == "null" {
		return nil, lookup
	}

	// Format array (baru)
	if strings.HasPrefix(raw, "[") {
		var channels []ChannelInfo
		if err := json.Unmarshal([]byte(raw), &channels); err == nil {
			for _, ch := range channels {
				if ch.Code == "" || ch.PGCode == "" {
					continue
				}
				lookup[strings.ToLower(strings.TrimSpace(ch.Code))] = ch.PGCode
			}
			return channels, lookup
		}
	}

	// Format object (lama): {"bca":"BCAVA"} -> auto-promote
	var kv map[string]string
	if err := json.Unmarshal([]byte(raw), &kv); err == nil {
		var channels []ChannelInfo
		for k, v := range kv {
			if k == "" || v == "" {
				continue
			}
			lookup[strings.ToLower(strings.TrimSpace(k))] = v
			channels = append(channels, ChannelInfo{
				Code:   strings.ToLower(k),
				PGCode: v,
				Label:  strings.ToUpper(k),
				Active: true,
			})
		}
		return channels, lookup
	}

	return nil, lookup
}

// ActiveChannels mengembalikan hanya channel dengan Active=true.
// Field sensitif (PGCode) tetap include — caller yang tentuin mau expose ke publik atau tidak.
func ActiveChannels(all []ChannelInfo) []ChannelInfo {
	out := make([]ChannelInfo, 0, len(all))
	for _, ch := range all {
		if ch.Active {
			out = append(out, ch)
		}
	}
	return out
}
