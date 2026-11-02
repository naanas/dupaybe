package repository

import (
	"dupay/internal/models"

	"gorm.io/gorm"
)

// EnsureUserRoleColumn nambah kolom users.role sebelum AutoMigrate. AutoMigrate nggak bisa nambah kolom
// NOT NULL tanpa default ke tabel yang udah ada isinya; user lama (sebelum ada role) semuanya admin.
func EnsureUserRoleColumn(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.User{}) || m.HasColumn(&models.User{}, "Role") {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`ALTER TABLE users ADD COLUMN role varchar(20) NOT NULL DEFAULT 'admin'`).Error; err != nil {
			return err
		}
		return tx.Exec(`ALTER TABLE users ALTER COLUMN role DROP DEFAULT`).Error
	})
}
