package models

import "time"

// --- CLIENT/MERCHANT SECTION ---
type ChargeRequest struct {
	OrderID       string  `json:"order_id" binding:"required"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Currency      string  `json:"currency" binding:"required"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
	GatewayName   string  `json:"gateway_name" binding:"required"`
}

type Transaction struct {
	ID               string    `json:"id" gorm:"primaryKey;type:uuid"`
	MerchantID       string    `json:"merchant_id" gorm:"type:varchar(255)"`
	PaymentGatewayID string    `json:"payment_gateway_id" gorm:"type:uuid"`
	OrderID          string    `json:"order_id" gorm:"type:varchar(255)"`
	Amount           float64   `json:"amount" gorm:"type:decimal(15,2)"`
	PaymentMethod    string    `json:"payment_method" gorm:"type:varchar(50)"`
	Status           string    `json:"status" gorm:"type:varchar(50)"`
	PGReferenceID    string    `json:"pg_reference_id" gorm:"type:varchar(255)"`
	CheckoutURL      string    `json:"checkout_url,omitempty" gorm:"type:text"`
	CreatedAt        time.Time `json:"created_at"`
}

type ChargeResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Data    Transaction `json:"data,omitempty"`
}

// --- ADMIN/CMS SECTION ---
type PaymentGateway struct {
	ID               string `gorm:"primaryKey;type:uuid;default:uuid_generate_v4()"`
	Name             string `gorm:"unique"`
	BaseURL          string
	ChargeEndpoint   string
	AuthType         string
	CustomAuthHeader string
	RequestTemplate  string `gorm:"type:jsonb"`
	ResponseMapping  string `gorm:"type:jsonb"`
	IsActive         bool   `gorm:"default:true"`
}

type Admin struct {
	ID       string `gorm:"primaryKey;type:uuid;default:uuid_generate_v4()"`
	Username string `gorm:"unique"`
	Password string `json:"-"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}
