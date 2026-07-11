// Package session menyimpan data sesi user SETELAH login berhasil.
//
// PENTING (prinsip BFF): browser TIDAK PERNAH menerima token Microsoft
// ataupun token Google secara langsung. Browser hanya memegang satu
// "session ID" acak lewat cookie httpOnly. Semua token disimpan di sini,
// di memori server.
//
// Untuk production sebaiknya diganti dengan store terpusat (Redis dsb),
// karena map in-memory ini hilang tiap kali server restart dan tidak
// bisa dipakai kalau backend di-scale ke banyak instance.
package session

import (
	"sync"
	"time"
)

type UserSession struct {
	// Identitas user, diambil dari ID Token Microsoft.
	Name  string
	Email string

	// Token Google hasil tukar (Workforce Identity Federation STS).
	// Inilah yang dipakai backend untuk memanggil Gemini Enterprise API
	// untuk general chat queries.
	GoogleAccessToken string
	GoogleTokenExpiry time.Time

	// Token Microsoft Graph API untuk connector access (Outlook data).
	// Didapat dari separate OAuth flow menggunakan Connector App Registration.
	ConnectorAccessToken  string
	ConnectorRefreshToken string
	ConnectorTokenExpiry  time.Time
	ConnectorAuthorized   bool

	CreatedAt time.Time
}

type Store struct {
	mu   sync.RWMutex
	data map[string]*UserSession
}

func NewStore() *Store {
	return &Store{data: make(map[string]*UserSession)}
}

func (s *Store) Set(sessionID string, sess *UserSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[sessionID] = sess
}

func (s *Store) Get(sessionID string) (*UserSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.data[sessionID]
	return sess, ok
}

func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, sessionID)
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]*UserSession)
}

// Count returns the number of active sessions
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// List returns all session IDs (for debugging only)
func (s *Store) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids
}
