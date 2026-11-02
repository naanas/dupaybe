package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Field callback iPaymu yang ditandatangani sebagai angka / boolean / array (docs iPaymu, bagian Callback).
var (
	ipaymuIntFields  = map[string]bool{"trx_id": true, "status_code": true, "transaction_status_code": true, "paid_off": true}
	ipaymuBoolFields = map[string]bool{"is_escrow": true}
)

// verifyIpaymuSignature ngecek header X-Signature callback iPaymu:
// hex(HMAC-SHA256(JSON body yang key-nya diurutin A-Z, key = nomor VA)).
// Docs-nya ambigu soal urutan key & escape "/", jadi beberapa varian dicoba.
func verifyIpaymuSignature(payload, signature, va string) bool {
	if signature == "" || va == "" {
		return false
	}
	fields := parseIpaymuCallback(payload)
	if fields == nil {
		return false
	}
	for _, sortKeys := range []func([]string){sortBytewise, sortLocaleLike} {
		jsonBody := marshalSorted(fields, sortKeys)
		for _, msg := range []string{strings.ReplaceAll(jsonBody, "/", `\/`), jsonBody} {
			mac := hmac.New(sha256.New, []byte(va))
			mac.Write([]byte(msg))
			if hmac.Equal([]byte(strings.ToLower(signature)), []byte(hex.EncodeToString(mac.Sum(nil)))) {
				return true
			}
		}
	}
	return false
}

// parseIpaymuCallback baca body callback (form-urlencoded default iPaymu, atau JSON) lalu
// nyamain tipe datanya kayak yang ditandatangani iPaymu.
func parseIpaymuCallback(payload string) map[string]interface{} {
	fields := map[string]interface{}{}
	trimmed := strings.TrimSpace(payload)
	if strings.HasPrefix(trimmed, "{") {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&fields); err != nil {
			return nil
		}
	} else {
		form, err := url.ParseQuery(trimmed)
		if err != nil || len(form) == 0 {
			return nil
		}
		for k, v := range form {
			fields[k] = v[0]
		}
	}

	for k, v := range fields {
		raw := strings.TrimSpace(strings.Trim(jsonScalar(v), `"`))
		switch {
		case ipaymuIntFields[k]:
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				fields[k] = n
			}
		case ipaymuBoolFields[k]:
			fields[k] = raw == "1" || strings.EqualFold(raw, "true")
		case k == "additional_info":
			if raw == "[]" || raw == "" {
				fields[k] = []interface{}{}
			}
		}
	}
	if _, ok := fields["additional_info"]; !ok {
		fields["additional_info"] = []interface{}{}
	}
	return fields
}

func jsonScalar(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// marshalSorted bikin JSON compact dengan urutan key top-level sesuai sortKeys, tanpa escape <>&.
func marshalSorted(fields map[string]interface{}, sortKeys func([]string)) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sortKeys(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(encodeJSON(k))
		buf.WriteByte(':')
		buf.WriteString(encodeJSON(fields[k]))
	}
	buf.WriteByte('}')
	return buf.String()
}

func encodeJSON(v interface{}) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

// sortBytewise = urutan A-Z case-sensitive (kayak ksort PHP).
func sortBytewise(keys []string) { sort.Strings(keys) }

// sortLocaleLike niru localeCompare JS di contoh docs iPaymu: nggak peduli huruf besar-kecil,
// tanda baca (mis. "_") duluan sebelum huruf/angka.
func sortLocaleLike(keys []string) {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "_", " ") }
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := norm(keys[i]), norm(keys[j])
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
}
