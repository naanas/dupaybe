package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyIpaymuSignature(t *testing.T) {
	va := "0000001234567890"
	form := "reference_id=ORDER1&status=berhasil&status_code=1&trx_id=245221&is_escrow=0&url=https%3A%2F%2Fx.com%2Fcb&total=13500"
	// Ditandatangani kayak contoh docs iPaymu: tipe dinormalisasi, key A-Z, "/" di-escape.
	msg := `{"additional_info":[],"is_escrow":false,"reference_id":"ORDER1","status":"berhasil","status_code":1,"total":"13500","trx_id":245221,"url":"https:\/\/x.com\/cb"}`
	mac := hmac.New(sha256.New, []byte(va))
	mac.Write([]byte(msg))
	sig := hex.EncodeToString(mac.Sum(nil))

	if !verifyIpaymuSignature(form, sig, va) {
		t.Fatal("signature valid ditolak")
	}
	if verifyIpaymuSignature(form, sig, "salah") {
		t.Fatal("VA salah diterima")
	}
	if verifyIpaymuSignature("reference_id=ORDER1&status=berhasil&status_code=1&trx_id=999&is_escrow=0&url=https%3A%2F%2Fx.com%2Fcb&total=13500", sig, va) {
		t.Fatal("body diubah diterima")
	}
	if verifyIpaymuSignature(form, "", va) {
		t.Fatal("tanpa signature diterima")
	}
}
