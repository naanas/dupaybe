package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func GenerateHMAC256(data string, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func VerifySignature(payload, timestamp, secret, clientSignature string) bool {
	expected := GenerateHMAC256(payload+timestamp, secret)
	return hmac.Equal([]byte(expected), []byte(clientSignature))
}
