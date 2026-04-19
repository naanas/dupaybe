package models

import "time"

// --- CLIENT/MERCHANT SECTION ---
type Merchant struct {
	ID             string `json:"id" gorm:"primaryKey;type:uuid;default:uuid_generate_v4()"`
	Name           string `json:"name" gorm:"unique;not null"`
	Email          string `json:"email" gorm:"unique"`
	Phone          string `json:"phone"`
	PICName        string `json:"pic_name"`
	APIKey         string `json:"api_key" gorm:"unique;not null;index"`
	SecretKey      string `json:"secret_key" gorm:"not null"`
	WhitelistedIPs string `json:"whitelisted_ips" gorm:"type:text"`
	WebhookURL     string `json:"webhook_url"`
	IsActive       bool   `json:"is_active" gorm:"default:true"`
}

type ChargeRequest struct {
	OrderID       string  `json:"order_id" binding:"required"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Currency      string  `json:"currency" binding:"required"`
	PaymentMethod string  `json:"payment_method" binding:"required"`
	GatewayName   string  `json:"gateway_name" binding:"required"`
}

type Transaction struct {
	ID               string    `json:"id" gorm:"primaryKey;type:uuid"`
	MerchantID       string    `json:"merchant_id" gorm:"type:uuid"`
	PaymentGatewayID string    `json:"payment_gateway_id" gorm:"type:uuid"`
	OrderID          string    `json:"order_id" gorm:"type:varchar(255)"`
	IdempotencyKey   string    `json:"idempotency_key" gorm:"type:varchar(255);index"`
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
	ID               string `json:"id" gorm:"primaryKey;type:uuid;default:uuid_generate_v4()"`
	Name             string `json:"name" gorm:"unique"`
	BaseURL          string `json:"base_url"`
	ChargeEndpoint   string `json:"charge_endpoint"`
	AuthType         string `json:"auth_type"`
	CustomAuthHeader string `json:"custom_auth_header"`

	// CREDENTIALS ENCRYPTED
	ServerKey    string `json:"server_key"`
	MerchantCode string `json:"merchant_code"` // BARU
	PrivateKey   string `json:"private_key"`   // BARU

	RequestTemplate       string `json:"request_template" gorm:"type:jsonb"`
	ResponseMapping       string `json:"response_mapping" gorm:"type:jsonb"`
	WebhookMapping        string `json:"webhook_mapping" gorm:"type:jsonb"`
	WebhookSecret         string `json:"webhook_secret"`
	WebhookValidationType string `json:"webhook_validation_type"`
	IsActive              bool   `json:"is_active" gorm:"default:true"`
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
