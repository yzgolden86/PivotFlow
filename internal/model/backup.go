package model

// BackupConfig stores the WebDAV target used by manual and scheduled portable
// configuration backups. The password is encrypted and never serialized.
type BackupConfig struct {
	ID                    int64  `json:"id"`
	Enabled               bool   `json:"enabled"`
	FileURL               string `json:"file_url"`
	Username              string `json:"username"`
	PasswordCiphertext    string `json:"-"`
	PasswordKeyVersion    string `json:"-"`
	PasswordConfigured    bool   `json:"password_configured"`
	ExportType            string `json:"export_type"`
	AutoSyncEnabled       bool   `json:"auto_sync_enabled"`
	AutoSyncIntervalHours int    `json:"auto_sync_interval_hours"`
	LastSyncAt            int64  `json:"last_sync_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	CreatedAt             int64  `json:"created_at"`
	UpdatedAt             int64  `json:"updated_at"`
}
