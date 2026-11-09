package outboundproxy

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gpt-load/internal/encryption"
	apierrors "gpt-load/internal/errors"
	"gpt-load/internal/httpclient"
	"gpt-load/internal/models"

	"gorm.io/gorm"
)

// InfrastructureError never exposes transport URLs or credentials.
type InfrastructureError struct {
	Status  int
	ProxyID uint
	Cause   error
}

func (e *InfrastructureError) Error() string {
	if e.ProxyID != 0 {
		return fmt.Sprintf("outbound proxy unavailable (status %d, proxy ID %d)", e.Status, e.ProxyID)
	}
	return fmt.Sprintf("outbound proxy unavailable (status %d)", e.Status)
}
func (e *InfrastructureError) Unwrap() error { return e.Cause }
func IsInfrastructure(err error) bool        { var e *InfrastructureError; return errors.As(err, &e) }

type Manager struct {
	mu       sync.RWMutex
	db       *gorm.DB
	enc      encryption.Service
	clients  *httpclient.HTTPClientManager
	snapshot []models.OutboundProxy
}

func NewManager(db *gorm.DB, enc encryption.Service, clients *httpclient.HTTPClientManager) *Manager {
	return &Manager{db: db, enc: enc, clients: clients}
}

func normalize(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "", apierrors.ErrValidation
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", apierrors.ErrValidation
	}
	if u.Opaque != "" || u.Host == "" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", apierrors.ErrValidation
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(host, " \t\r\n/\\%") {
		return "", apierrors.ErrValidation
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", apierrors.ErrValidation
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return "", apierrors.ErrValidation
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", apierrors.ErrValidation
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", apierrors.ErrValidation
				}
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", apierrors.ErrValidation
	}
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", apierrors.ErrValidation
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") || ((u.Scheme == "socks5" || u.Scheme == "socks5h") && port == "1080") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Path = ""
	u.RawPath = ""
	return u.String(), nil
}
func (m *Manager) load(db *gorm.DB) ([]models.OutboundProxy, error) {
	var rows []models.OutboundProxy
	if err := db.Find(&rows).Error; err != nil {
		return nil, apierrors.ErrDatabase
	}
	for i := range rows {
		plain, err := m.enc.Decrypt(rows[i].URL)
		if err != nil || m.enc.Hash(plain) != rows[i].URLHash {
			return nil, fmt.Errorf("outbound proxy %d: encryption/hash mismatch", rows[i].ID)
		}
		rows[i].URL, err = normalize(plain)
		if err != nil {
			return nil, apierrors.ErrInternalServer
		}
	}
	return rows, nil
}
func (m *Manager) publish(rows []models.OutboundProxy) {
	for _, old := range m.snapshot {
		keep := false
		for _, next := range rows {
			if old.ID == next.ID && old.URL == next.URL && next.Enabled {
				keep = true
				break
			}
		}
		if !keep {
			m.clients.EvictProxy(old.URL)
		}
	}
	m.snapshot = rows
}
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.load(m.db)
	if err == nil {
		m.publish(rows)
	}
	return err
}
func (m *Manager) List() ([]models.OutboundProxy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]models.OutboundProxy{}, m.snapshot...), nil
}
func (m *Manager) Select(keyHash string) (models.OutboundProxy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.selectLocked(keyHash)
}

func (m *Manager) ClientFor(keyHash string, config httpclient.Config) (*http.Client, uint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proxy, err := m.selectLocked(keyHash)
	if err != nil {
		return nil, 0, err
	}
	config.ProxyURL = proxy.URL
	return m.clients.GetClient(&config), proxy.ID, nil
}

func (m *Manager) selectLocked(keyHash string) (models.OutboundProxy, error) {
	var best models.OutboundProxy
	var score [32]byte
	found := false
	if keyHash != "" {
		for _, p := range m.snapshot {
			if !p.Enabled {
				continue
			}
			s := sha256.Sum256([]byte(keyHash + "\x00" + strconv.FormatUint(uint64(p.ID), 10)))
			c := bytes.Compare(s[:], score[:])
			if !found || c > 0 || (c == 0 && p.ID < best.ID) {
				best = p
				score = s
				found = true
			}
		}
	}
	if !found {
		return best, &InfrastructureError{Status: 503}
	}
	return best, nil
}

// IndexedValidationError identifies an invalid batch item without exposing its contents.
type IndexedValidationError struct {
	Index int
}

func (e *IndexedValidationError) Error() string { return "invalid outbound proxy batch item" }

type BatchItem struct {
	URL     string
	Enabled *bool
}

type BatchResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

func (m *Manager) CreateBatch(items []BatchItem) (*BatchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(items) == 0 || len(items) > 500 {
		return nil, apierrors.ErrValidation
	}
	// Validate every item before looking for duplicates or writing anything.
	prepared := make([]models.OutboundProxy, len(items))
	for i, item := range items {
		normalized, err := normalize(strings.TrimSpace(item.URL))
		if err != nil {
			return nil, &IndexedValidationError{Index: i}
		}
		u, err := url.Parse(normalized)
		if err != nil || utf8.RuneCountInString(u.Host) > 255 {
			return nil, &IndexedValidationError{Index: i}
		}
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		prepared[i] = models.OutboundProxy{Name: u.Host, URL: normalized, URLHash: m.enc.Hash(normalized), Enabled: enabled}
	}
	result := &BatchResult{}
	var rows []models.OutboundProxy
	err := m.db.Transaction(func(tx *gorm.DB) error {
		hashes := make([]string, len(prepared))
		for i, item := range prepared {
			hashes[i] = item.URLHash
		}
		var existing []string
		if err := tx.Model(&models.OutboundProxy{}).Where("url_hash IN ?", hashes).Pluck("url_hash", &existing).Error; err != nil {
			return apierrors.ErrDatabase
		}
		seen := make(map[string]bool, len(existing)+len(prepared))
		for _, hash := range existing {
			seen[hash] = true
		}
		inserts := make([]models.OutboundProxy, 0, len(prepared))
		for _, item := range prepared {
			if seen[item.URLHash] {
				result.Skipped++
				continue
			}
			seen[item.URLHash] = true
			ciphertext, err := m.enc.Encrypt(item.URL)
			if err != nil {
				return apierrors.ErrInternalServer
			}
			item.URL = ciphertext
			inserts = append(inserts, item)
		}
		result.Created = len(inserts)
		if result.Created == 0 {
			return nil
		}
		if err := tx.Select("Name", "URL", "URLHash", "Enabled", "CreatedAt", "UpdatedAt").CreateInBatches(&inserts, 100).Error; err != nil {
			return apierrors.ErrDatabase
		}
		var err error
		rows, err = m.load(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result.Created > 0 {
		m.publish(rows)
	}
	return result, nil
}

func (m *Manager) Create(name, raw string, enabled bool) (*models.OutboundProxy, error) {
	return m.mutate(0, &name, &raw, &enabled, false)
}
func (m *Manager) Update(id uint, name, raw *string, enabled *bool) (*models.OutboundProxy, error) {
	if id == 0 {
		return nil, apierrors.ErrResourceNotFound
	}
	return m.mutate(id, name, raw, enabled, false)
}
func (m *Manager) Delete(id uint) error { _, err := m.mutate(id, nil, nil, nil, true); return err }
func (m *Manager) mutate(id uint, name, raw *string, enabled *bool, remove bool) (*models.OutboundProxy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result models.OutboundProxy
	var rows []models.OutboundProxy
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if id != 0 || remove {
			if err := tx.First(&result, id).Error; err != nil {
				return apierrors.ParseDBError(err)
			}
		}
		if remove {
			if err := tx.Delete(&result).Error; err != nil {
				return apierrors.ParseDBError(err)
			}
		} else {
			if name != nil {
				result.Name = strings.TrimSpace(*name)
			}
			if result.Name == "" || utf8.RuneCountInString(result.Name) > 255 {
				return apierrors.ErrValidation
			}
			if raw != nil {
				normalized, err := normalize(*raw)
				if err != nil {
					return err
				}
				result.URLHash = m.enc.Hash(normalized)
				var count int64
				if err = tx.Model(&models.OutboundProxy{}).Where("url_hash = ? AND id <> ?", result.URLHash, id).Count(&count).Error; err != nil {
					return apierrors.ErrDatabase
				}
				if count > 0 {
					return apierrors.ErrDuplicateResource
				}
				result.URL, err = m.enc.Encrypt(normalized)
				if err != nil {
					return apierrors.ErrInternalServer
				}
			}
			if enabled != nil {
				result.Enabled = *enabled
			}
			if id == 0 {
				if err := tx.Select("Name", "URL", "URLHash", "Enabled").Create(&result).Error; err != nil {
					return apierrors.ParseDBError(err)
				}
			} else {
				if err := tx.Model(&result).Select("Name", "URL", "URLHash", "Enabled").Updates(&result).Error; err != nil {
					return apierrors.ParseDBError(err)
				}
			}
		}
		var err error
		rows, err = m.load(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	m.publish(rows)
	if remove {
		return nil, nil
	}
	for _, p := range rows {
		if p.ID == result.ID {
			copy := p
			return &copy, nil
		}
	}
	return nil, apierrors.ErrInternalServer
}
