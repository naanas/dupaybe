package middleware

import (
	"bytes"
	"io"
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

// bodyLogWriter digunakan untuk membajak/mencegat Response agar bisa dibaca sebelum dikirim ke Klien
type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// APILogger adalah middleware untuk mencatat Request dan Response lengkap
func APILogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		// 1. BACA REQUEST BODY
		var reqBodyBytes []byte
		if c.Request.Body != nil {
			reqBodyBytes, _ = io.ReadAll(c.Request.Body)
			// KEMBALIKAN BODY KE REQUEST (PENTING! Karena body di Go cuma bisa dibaca sekali)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
		}

		// 2. SIAPKAN PENANGKAP RESPONSE
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		// 3. LANJUTKAN PROSES KE HANDLER (Proses Charge / Webhook / CMS)
		c.Next()

		// 4. SETELAH SELESAI, TULIS KE LOG TERMINAL
		duration := time.Since(startTime)

		log.Printf("\n==================== [ API LOG ] ====================")
		log.Printf("[REQ] %s %s", c.Request.Method, c.Request.URL.Path)

		// Hanya tampilkan request body kalau ada isinya
		if len(reqBodyBytes) > 0 {
			log.Printf("[REQ BODY] %s", string(reqBodyBytes))
		}

		log.Printf("-----------------------------------------------------")
		log.Printf("[RSP] Status: %d | Duration: %v", c.Writer.Status(), duration)

		// Tampilkan response body
		if blw.body.Len() > 0 {
			log.Printf("[RSP BODY] %s", blw.body.String())
		}
		log.Printf("=====================================================\n")
	}
}
