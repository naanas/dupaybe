package service

import (
	"dupay/internal/models"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// Status hasil penggabungan channel dari PG dengan channel_mapping yang udah ada.
const (
	ChannelSyncNew       = "new"       // ada di PG, belum ada di Dupay
	ChannelSyncUpdated   = "updated"   // ada di dua-duanya, ada field yang berubah
	ChannelSyncUnchanged = "unchanged" // ada di dua-duanya, sama persis
	ChannelSyncMissing   = "missing"   // ada di Dupay, nggak ada lagi di PG (dinonaktifin)
)

type ChannelSyncItem struct {
	ChannelInfo
	SyncState string `json:"sync_state"`
}

// FetchPGChannels narik daftar channel dari endpoint "channels" gateway (isi form CMS, lihat resolveDraftGateway)
// lalu digabung dengan channel_mapping di form. Hasilnya cuma preview, nggak disimpan: admin cek dulu baru save.
func (s *chargeService) FetchPGChannels(draft models.PaymentGateway) ([]ChannelSyncItem, error) {
	pgp, err := s.resolveDraftGateway(draft)
	if err != nil {
		return nil, err
	}
	pg := *pgp

	ep := resolveEndpoint(&pg, models.EndpointActionChannels, "")
	if ep == nil {
		return nil, fmt.Errorf("gateway %s belum punya endpoint Daftar Channel yang aktif", pg.Name)
	}

	res, err := s.callGateway(&pg, ep, pgCallVars{MerchantCode: pg.MerchantCode})
	if err != nil {
		return nil, err
	}
	statusCode, respStr := res.StatusCode, res.Body
	if statusCode >= 400 {
		return nil, fmt.Errorf("Ditolak oleh %s (Code: %d): %s", pg.Name, statusCode, respStr)
	}

	var mapping map[string]string
	if err := json.Unmarshal([]byte(ep.ResponseMapping), &mapping); err != nil || mapping["pg_code"] == "" {
		return nil, errors.New("response mapping endpoint Daftar Channel wajib punya key pg_code")
	}

	fromPG, err := parseChannelList(respStr, mapping)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", pg.Name, err)
	}

	existing, _ := ParseChannels(pg.ChannelMapping)
	return mergeChannels(existing, fromPG), nil
}

// parseChannelList baca daftar channel dari response PG pakai response mapping endpoint "channels".
// Key mapping: list (path array-nya), pg_code (wajib), label, group, logo, min_amount, max_amount,
// fee_flat, fee_percent, active. Path di dalam item boleh pakai "|" buat fallback.
func parseChannelList(respStr string, mapping map[string]string) ([]ChannelInfo, error) {
	list := gjson.Parse(respStr)
	if mapping["list"] != "" {
		list = gjson.Get(respStr, mapping["list"])
	}
	if !list.IsArray() {
		return nil, fmt.Errorf("path list %q di response bukan array. Response: %s", mapping["list"], respStr)
	}

	var fromPG []ChannelInfo
	for _, item := range list.Array() {
		get := func(key string) gjson.Result {
			for _, p := range strings.Split(mapping[key], "|") {
				if p = strings.TrimSpace(p); p != "" {
					if v := item.Get(p); v.Exists() && v.String() != "" {
						return v
					}
				}
			}
			return gjson.Result{}
		}

		pgCode := strings.TrimSpace(get("pg_code").String())
		if pgCode == "" {
			continue
		}
		active := true
		if v := get("active"); v.Exists() {
			active = v.Bool()
		}
		fromPG = append(fromPG, ChannelInfo{
			PGCode:     pgCode,
			Label:      get("label").String(),
			Group:      get("group").String(),
			Logo:       get("logo").String(),
			MinAmount:  get("min_amount").Int(),
			MaxAmount:  get("max_amount").Int(),
			FeeFlat:    get("fee_flat").Int(),
			FeePercent: get("fee_percent").Float(),
			Active:     active,
		})
	}
	if len(fromPG) == 0 {
		return nil, fmt.Errorf("nggak ada channel yang kebaca dari response, cek response mapping. Response: %s", respStr)
	}
	return fromPG, nil
}

// mergeChannels gabungin channel dari PG dengan yang udah ada, dicocokin lewat pg_code.
// Yang dipertahankan dari data lama: kode internal (dipakai merchant), jenis channel, label, grup, logo,
// dan channel yang sengaja dimatiin admin. Fee, min/max, & status aktif di PG diambil dari PG.
func mergeChannels(existing []ChannelInfo, fromPG []ChannelInfo) []ChannelSyncItem {
	byPGCode := map[string]int{}
	usedCodes := map[string]bool{}
	for i, ch := range existing {
		byPGCode[strings.ToUpper(ch.PGCode)] = i
		usedCodes[strings.ToLower(ch.Code)] = true
	}

	matched := map[int]bool{}
	out := make([]ChannelSyncItem, 0, len(fromPG)+len(existing))

	for _, p := range fromPG {
		method, group := guessChannelType(p.PGCode, p.Label, p.Group)

		idx, ok := byPGCode[strings.ToUpper(p.PGCode)]
		if !ok {
			ch := p
			ch.Code = uniqueCode(strings.ToLower(p.PGCode), usedCodes)
			ch.Method = method
			ch.Group = group
			out = append(out, ChannelSyncItem{ChannelInfo: ch, SyncState: ChannelSyncNew})
			continue
		}

		matched[idx] = true
		old := existing[idx]
		ch := old
		ch.MinAmount, ch.MaxAmount = p.MinAmount, p.MaxAmount
		ch.FeeFlat, ch.FeePercent = p.FeeFlat, p.FeePercent
		ch.Active = old.Active && p.Active // dimatiin admin tetap mati
		if ch.Label == "" {
			ch.Label = p.Label
		}
		if ch.Logo == "" {
			ch.Logo = p.Logo
		}
		if ch.Method == "" {
			ch.Method = method
		}
		if ch.Group == "" {
			ch.Group = group
		}

		state := ChannelSyncUnchanged
		if ch != old {
			state = ChannelSyncUpdated
		}
		out = append(out, ChannelSyncItem{ChannelInfo: ch, SyncState: state})
	}

	for i, old := range existing {
		if matched[i] {
			continue
		}
		ch := old
		ch.Active = false
		out = append(out, ChannelSyncItem{ChannelInfo: ch, SyncState: ChannelSyncMissing})
	}
	return out
}

// guessChannelType nebak jenis channel (va/qris/ewallet/cstore) & grup tampilan dari data PG.
// Cuma tebakan; admin tetap harus cek di CMS karena jenis channel nentuin endpoint charge yang dipakai.
func guessChannelType(pgCode, label, group string) (method string, displayGroup string) {
	all := strings.ToLower(pgCode + " " + label + " " + group)
	switch {
	case strings.Contains(all, "qris"):
		return "qris", "QRIS"
	case strings.Contains(all, "virtual") || strings.HasSuffix(strings.ToLower(pgCode), "va"):
		return "va", "Virtual Account"
	case strings.Contains(all, "wallet"):
		return "ewallet", "E-Wallet"
	case strings.Contains(all, "convenience") || strings.Contains(all, "retail") ||
		strings.Contains(all, "store") || strings.Contains(all, "alfamart") || strings.Contains(all, "indomaret"):
		return "cstore", "Retail"
	}
	return "", ""
}

func uniqueCode(base string, used map[string]bool) string {
	code := base
	for n := 2; used[code]; n++ {
		code = fmt.Sprintf("%s_%d", base, n)
	}
	used[code] = true
	return code
}
