package store

import (
	"database/sql"
	"time"

	"relayscope/internal/domain"
)

type Store struct {
	db *sql.DB
}

type Site struct {
	ID                  int64                   `json:"id"`
	Name                string                  `json:"name"`
	BaseURL             string                  `json:"baseUrl"`
	SourceURL           string                  `json:"sourceUrl"`
	AdapterKey          string                  `json:"adapterKey"`
	AdapterConfig       string                  `json:"adapterConfig"`
	CustomFailureReason string                  `json:"customFailureReason"`
	Enabled             bool                    `json:"enabled"`
	SessionRequired     bool                    `json:"sessionRequired"`
	Interval            time.Duration           `json:"interval"`
	Jitter              time.Duration           `json:"jitter"`
	IntervalSeconds     int64                   `json:"intervalSeconds"`
	JitterSeconds       int64                   `json:"jitterSeconds"`
	SessionConfigured   bool                    `json:"sessionConfigured"`
	AcquisitionState    domain.AcquisitionState `json:"acquisitionState"`
	NextRunAt           *time.Time              `json:"nextRunAt,omitempty"`
	DeletedAt           *time.Time              `json:"deletedAt,omitempty"`
	CreatedAt           time.Time               `json:"createdAt"`
	UpdatedAt           time.Time               `json:"updatedAt"`
}
type FailureAnnouncement struct {
	SiteID      int64  `json:"siteId"`
	SiteName    string `json:"siteName"`
	FailureCode string `json:"failureCode"`
	Reason      string `json:"reason"`
}
type CollectionRun struct {
	ID              int64      `json:"id"`
	SiteID          int64      `json:"siteId"`
	SiteName        string     `json:"siteName"`
	AdapterKey      string     `json:"adapterKey"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Status          string     `json:"status"`
	CatalogComplete bool       `json:"catalogComplete"`
	ModelsSeen      int        `json:"modelsSeen"`
	GroupsSeen      int        `json:"groupsSeen"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
}
type EncryptedSession struct {
	SiteID     int64
	Purpose    string
	KeyVersion int
	Nonce      []byte
	Ciphertext []byte
	ExpiresAt  *time.Time
	UpdatedAt  time.Time
}

type SessionMetadata struct {
	SiteID          int64      `json:"siteId"`
	Purpose         string     `json:"purpose"`
	KeyVersion      int        `json:"keyVersion"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	NonceBytes      int        `json:"nonceBytes"`
	CiphertextBytes int        `json:"ciphertextBytes"`
}

type UnmatchedModel struct {
	SiteID       int64     `json:"siteId"`
	SiteName     string    `json:"siteName"`
	RawModelName string    `json:"rawModelName"`
	ProviderHint string    `json:"providerHint"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

type RunFilters struct {
	Limit  int
	SiteID int64
	Status string
	Since  *time.Time
}
type siteRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}
