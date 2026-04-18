package models

import "time"

// Payload request dari Klien
type ChargeRequest struct {
	OrderID       string  `json:"order_id" binding:"required"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Currency      string  `json:"currency" binding:"required"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
}

// Representasi Tabel di Supabase
type Transaction struct {
	ID               string    `json:"id" gorm:"primaryKey;type:uuid"`
	MerchantID       string    `json:"merchant_id" gorm:"type:varchar(255)"`
	PaymentGatewayID string    `json:"payment_gateway_id" gorm:"type:varchar(100)"`
	OrderID          string    `json:"order_id" gorm:"type:varchar(255)"`
	Amount           float64   `json:"amount" gorm:"type:decimal(15,2)"`
	PaymentMethod    string    `json:"payment_method" gorm:"type:varchar(50)"`
	Status           string    `json:"status" gorm:"type:varchar(50)"`
	CheckoutURL      string    `json:"checkout_url,omitempty" gorm:"type:text"`
	CreatedAt        time.Time `json:"created_at"`
}

// Balikan API
type ChargeResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Data    Transaction `json:"data,omitempty"`
}
