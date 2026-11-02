package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"dupay/internal/models"
	"dupay/pkg/crypto"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// pgCallVars = nilai yang bisa dipakai di path & request template endpoint lewat placeholder
// {{order_id}}, {{amount}}, {{payment_method}}, {{pg_reference_id}}, {{merchant_code}}.
type pgCallVars struct {
	OrderID       string
	Amount        float64
	PaymentMethod string // kode PG hasil channel_mapping (mis. "BCAVA")
	PGReferenceID string
	MerchantCode  string
}

// PGCallResult = request yang beneran dikirim ke PG + response-nya (dipakai juga buat fitur Test di CMS).
type PGCallResult struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	RequestBody string `json:"request_body"`
	StatusCode  int    `json:"status_code"`
	Body        string `json:"body"`
}

// resolveEndpoint nyari endpoint aktif buat sebuah action. Urutan prioritas:
//  1. endpoint yang channel_method-nya sama persis (mis. "va")
//  2. endpoint "semua channel" (channel_method kosong)
//  3. khusus charge: kolom lama charge_endpoint/request_template/response_mapping
func resolveEndpoint(pg *models.PaymentGateway, action string, channelMethod string) *models.GatewayEndpoint {
	var fallback *models.GatewayEndpoint
	for i := range pg.Endpoints {
		ep := &pg.Endpoints[i]
		if !ep.IsActive || ep.Action != action {
			continue
		}
		if channelMethod != "" && strings.EqualFold(ep.ChannelMethod, channelMethod) {
			return ep
		}
		if ep.ChannelMethod == "" && fallback == nil {
			fallback = ep
		}
	}
	if fallback == nil && action == models.EndpointActionCharge && pg.ChargeEndpoint != "" {
		return &models.GatewayEndpoint{
			Action:          models.EndpointActionCharge,
			HTTPMethod:      http.MethodPost,
			Path:            pg.ChargeEndpoint,
			RequestTemplate: pg.RequestTemplate,
			ResponseMapping: pg.ResponseMapping,
			IsActive:        true,
		}
	}
	return fallback
}

// findChannel nyari channel di channel_mapping berdasarkan kode internal client (mis. "bca").
// Kalau nggak ada di mapping, kode dikirim apa adanya (huruf besar/kecil nggak diubah, kode PG biasanya case-sensitive).
func findChannel(pg *models.PaymentGateway, code string) (pgCode string, method string) {
	code = strings.TrimSpace(code)
	pgCode = code
	channels, lookup := ParseChannels(pg.ChannelMapping)
	if v, ok := lookup[strings.ToLower(code)]; ok && v != "" {
		pgCode = v
	}
	for _, ch := range channels {
		if strings.EqualFold(strings.TrimSpace(ch.Code), code) {
			method = ch.Method
			break
		}
	}
	return pgCode, method
}

// renderTemplate ganti placeholder pakai vars. escape dipakai buat nilai string
// (JSON-escape buat body, URL-escape buat path). "{{amount}}" yang dikutip jadi angka JSON.
func renderTemplate(tpl string, v pgCallVars, escape func(string) string) string {
	amount := fmt.Sprintf("%.0f", v.Amount)
	tpl = strings.ReplaceAll(tpl, "\"{{amount}}\"", amount)
	return strings.NewReplacer(
		"{{order_id}}", escape(v.OrderID),
		"{{payment_method}}", escape(v.PaymentMethod),
		"{{pg_reference_id}}", escape(v.PGReferenceID),
		"{{merchant_code}}", escape(v.MerchantCode),
		"{{amount}}", amount,
	).Replace(tpl)
}

func jsonStringContent(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// extractPaths ambil value pertama yang non-empty dari beberapa path gjson yang dipisah "|".
// Contoh: "data.pay_code|data.qr_string|data.qr_url".
func extractPaths(body string, paths string) string {
	if paths == "" {
		return ""
	}
	for _, p := range strings.Split(paths, "|") {
		if v := gjson.Get(body, strings.TrimSpace(p)).String(); v != "" {
			return v
		}
	}
	return ""
}

// Batas nunggu balasan PG. Sandbox (mis. iPaymu bikin VA) bisa >10 detik.
const pgRequestTimeout = 30 * time.Second

// callGateway merakit & ngirim request ke PG buat satu endpoint, termasuk auth/signature sesuai auth_type.
func (s *chargeService) callGateway(pg *models.PaymentGateway, ep *models.GatewayEndpoint, v pgCallVars) (*PGCallResult, error) {
	method := strings.ToUpper(strings.TrimSpace(ep.HTTPMethod))
	if method == "" {
		method = http.MethodPost
	}

	path := renderTemplate(ep.Path, v, url.QueryEscape)
	targetURL := strings.TrimSuffix(pg.BaseURL, "/") + "/" + strings.TrimPrefix(path, "/")

	body := ""
	if method != http.MethodGet {
		body = strings.TrimSpace(renderTemplate(ep.RequestTemplate, v, jsonStringContent))
	}

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

	headers := map[string]string{}
	switch pg.AuthType {
	case "TRIPAY_HMAC":
		// Signature Tripay (merchant_code + merchant_ref + amount) cuma dipakai pas bikin transaksi.
		if ep.Action == models.EndpointActionCharge && strings.HasSuffix(body, "}") {
			sigString := fmt.Sprintf("%s%s%.0f", pg.MerchantCode, v.OrderID, v.Amount)
			signature := crypto.GenerateHMAC256(sigString, decryptedPrivateKey)
			inner := strings.TrimSpace(body[:len(body)-1])
			sep := ", "
			if strings.HasSuffix(inner, "{") {
				sep = ""
			}
			body = inner + sep + fmt.Sprintf(`"signature": "%s"}`, signature)
		}
		headers["Authorization"] = "Bearer " + decryptedServerKey

	case "IPAYMU_V2":
		buffer := new(bytes.Buffer)
		if err := json.Compact(buffer, []byte(body)); err == nil {
			body = buffer.String()
		}

		bodyHasher := sha256.New()
		bodyHasher.Write([]byte(body))
		bodyHashStr := hex.EncodeToString(bodyHasher.Sum(nil))

		stringToSign := fmt.Sprintf("%s:%s:%s:%s", method, pg.MerchantCode, bodyHashStr, decryptedPrivateKey)

		mac := hmac.New(sha256.New, []byte(decryptedPrivateKey))
		mac.Write([]byte(stringToSign))

		headers["va"] = pg.MerchantCode
		headers["signature"] = hex.EncodeToString(mac.Sum(nil))
		headers["timestamp"] = time.Now().Format("20060102150405")

	case "BEARER_TOKEN", "Bearer Auth":
		headers["Authorization"] = "Bearer " + decryptedServerKey
	case "CUSTOM_HEADER":
		if pg.CustomAuthHeader != "" {
			headers[pg.CustomAuthHeader] = decryptedServerKey
		}
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	httpReq, err := http.NewRequest(method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("gagal merakit HTTP request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, val := range headers {
		httpReq.Header.Set(k, val)
	}
	if pg.AuthType == "BASIC_AUTH" {
		httpReq.SetBasicAuth(decryptedServerKey, "")
	}

	client := &http.Client{Timeout: pgRequestTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi PG: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return &PGCallResult{
		Method:      method,
		URL:         targetURL,
		RequestBody: body,
		StatusCode:  resp.StatusCode,
		Body:        string(respBody),
	}, nil
}

// mapStatus terjemahin status PG ke status internal pakai webhook_mapping
// (success_statuses / failed_statuses / refunded_statuses). Selain itu = PENDING.
func mapStatus(mapping map[string]interface{}, pgStatus string) string {
	for _, rule := range []struct{ key, status string }{
		{"success_statuses", "SUCCESS"},
		{"failed_statuses", "FAILED"},
		{"refunded_statuses", "REFUNDED"},
	} {
		list, _ := mapping[rule.key].([]interface{})
		for _, st := range list {
			if s, ok := st.(string); ok && strings.EqualFold(s, pgStatus) {
				return rule.status
			}
		}
	}
	return "PENDING"
}
