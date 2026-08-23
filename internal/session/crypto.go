package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"relaypulse/internal/store"
)

const SessionPurpose = "site-http"

const (
	AuthTypeNewAPIToken  = "newapi_token"
	AuthTypeSub2APIToken = "sub2api_token"
	legacyAccessToken    = "access_token"
)

type Cookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type Data struct {
	AuthType       string   `json:"authType,omitempty"`
	AccessToken    string   `json:"accessToken,omitempty"`
	UserID         string   `json:"userId,omitempty"`
	RefreshToken   string   `json:"refreshToken,omitempty"`
	TokenExpiresAt int64    `json:"tokenExpiresAt,omitempty"`
	UserAgent      string   `json:"userAgent,omitempty"`
	Cookies        []Cookie `json:"cookies,omitempty"`
}

type Vault struct{ key []byte }

func NewVault(rawKey string) (*Vault, error) {
	value := strings.TrimSpace(rawKey)
	if value == "" {
		return nil, errors.New("session encryption key is required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) < 32 {
		return nil, errors.New("session encryption key must be at least 32 base64url bytes")
	}
	hash := sha256.Sum256(decoded)
	return &Vault{key: hash[:]}, nil
}

func (vault *Vault) Encrypt(data Data) (nonce, ciphertext []byte, err error) {
	if vault == nil || len(vault.key) != 32 {
		return nil, nil, errors.New("session vault is not configured")
	}
	if err := validateData(data); err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(vault.key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, nil, fmt.Errorf("encode session payload: %w", err)
	}
	return nonce, gcm.Seal(nil, nonce, encoded, nil), nil
}

func validateData(data Data) error {
	if data.AuthType == "" && data.AccessToken != "" {
		data.AuthType = AuthTypeNewAPIToken
	}
	if data.AuthType != "" && data.AuthType != legacyAccessToken && data.AuthType != AuthTypeNewAPIToken && data.AuthType != AuthTypeSub2APIToken {
		return errors.New("unsupported session authentication type")
	}
	if data.AuthType == legacyAccessToken || data.AuthType == AuthTypeNewAPIToken || data.AuthType == AuthTypeSub2APIToken {
		if strings.TrimSpace(data.AccessToken) == "" {
			return errors.New("access token is required")
		}
		if (data.AuthType == legacyAccessToken || data.AuthType == AuthTypeNewAPIToken) && strings.TrimSpace(data.UserID) == "" {
			return errors.New("NewAPI access token user ID is required")
		}
		if data.AuthType == AuthTypeSub2APIToken && (strings.TrimSpace(data.RefreshToken) == "" || data.TokenExpiresAt <= 0) {
			return errors.New("Sub2API refresh token and expiry are required")
		}
		if len(data.AccessToken) > 8192 || len(data.UserID) > 128 || len(data.RefreshToken) > 8192 || strings.ContainsAny(data.AccessToken+data.UserID+data.RefreshToken, "\r\n") {
			return errors.New("invalid access token credentials")
		}
		if data.TokenExpiresAt < 0 {
			return errors.New("invalid access token expiry")
		}
	} else if len(data.Cookies) == 0 {
		return errors.New("session cookies or access token are required")
	}
	if len(data.Cookies) > 80 {
		return errors.New("too many session cookies")
	}
	if len(data.UserAgent) > 1024 {
		return errors.New("session user agent is too long")
	}
	for _, cookie := range data.Cookies {
		if cookie.Name == "" || len(cookie.Name) > 256 || strings.ContainsAny(cookie.Name, "\r\n;=") || strings.ContainsAny(cookie.Value, "\r\n") || len(cookie.Value) > 8192 {
			return errors.New("invalid session cookie")
		}
	}
	return nil
}

func (vault *Vault) Decrypt(nonce, ciphertext []byte) (Data, error) {
	if vault == nil || len(vault.key) != 32 {
		return Data{}, errors.New("session vault is not configured")
	}
	block, err := aes.NewCipher(vault.key)
	if err != nil {
		return Data{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Data{}, err
	}
	if len(nonce) != gcm.NonceSize() {
		return Data{}, errors.New("invalid session nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Data{}, errors.New("invalid session ciphertext")
	}
	var data Data
	if err := json.Unmarshal(plain, &data); err != nil {
		return Data{}, fmt.Errorf("decode session payload: %w", err)
	}
	return data, nil
}

func (vault *Vault) Save(ctx context.Context, db *store.Store, siteID int64, data Data, expiresAt *time.Time) error {
	return vault.SaveBatch(ctx, db, []BatchItem{{SiteID: siteID, Data: data}}, expiresAt)
}

type BatchItem struct {
	SiteID int64
	Data   Data
}

func (vault *Vault) SaveBatch(ctx context.Context, db *store.Store, items []BatchItem, expiresAt *time.Time) error {
	sessions := make([]store.EncryptedSession, 0, len(items))
	for _, item := range items {
		nonce, ciphertext, err := vault.Encrypt(item.Data)
		if err != nil {
			return err
		}
		sessions = append(sessions, store.EncryptedSession{SiteID: item.SiteID, Purpose: SessionPurpose, KeyVersion: 1, Nonce: nonce, Ciphertext: ciphertext, ExpiresAt: expiresAt})
	}
	return db.SaveEncryptedSessions(ctx, sessions)
}

func (vault *Vault) Load(ctx context.Context, db *store.Store, siteID int64) (Data, *time.Time, error) {
	session, err := db.LoadEncryptedSession(ctx, siteID, SessionPurpose)
	if err != nil {
		return Data{}, nil, err
	}
	if session.ExpiresAt != nil && session.ExpiresAt.Before(time.Now().UTC()) {
		return Data{}, session.ExpiresAt, errors.New("session expired")
	}
	data, err := vault.Decrypt(session.Nonce, session.Ciphertext)
	return data, session.ExpiresAt, err
}
