# Goole-GE-x-Microsoft-Tools
# cngpt-bff-sso

Contoh backend Go + [Fiber](https://gofiber.io) **sesederhana mungkin** untuk
mempraktikkan pola **BFF (Backend For Frontend) SSO**:

```
Browser -> Backend (Go) -> Microsoft Entra ID -> Backend -> Google STS -> Backend -> Gemini Enterprise
```

Sesuai keputusan arsitektur di Bagian 6 dokumentasi: **browser tidak pernah
memegang token Microsoft maupun Google.** Browser hanya mendapat satu
session cookie `httpOnly`. Semua token disimpan & dipakai di server.

## Alur (10 langkah, sama seperti di dokumentasi)

1. Browser buka frontend, klik "Login"
2. Frontend redirect ke `GET /auth/login` (backend ini)
3. Backend redirect ke Microsoft (pakai App Registration "Web" + client secret)
4. User login di halaman Microsoft
5. Microsoft redirect balik ke `GET /auth/callback?code=...`
6. Backend tukar `code` → ID Token (server-to-server, pakai client secret)
7. Backend tukar ID Token → Google Access Token (lewat Google STS /
   Workforce Identity Federation, pool `cngpt-entra-pool`)
8. Backend simpan session di memori, kirim cookie `httpOnly` ke browser
9. Browser diarahkan ke frontend, sudah "login" tanpa pernah pegang token
10. Frontend panggil `POST /api/chat`, backend yang meneruskan ke Gemini
    Enterprise pakai Google Access Token yang tersimpan di server

## Struktur folder

```
cngpt-bff-sso/
├── main.go                     # entrypoint, wiring routes
├── internal/
│   ├── config/config.go        # baca environment variable
│   ├── entra/entra.go          # OAuth ke Microsoft Entra ID
│   ├── gcpsts/sts.go           # tukar token ke Google STS (Workforce Identity Federation)
│   ├── gemini/gemini.go        # panggil Gemini Enterprise API (streamAssist)
│   ├── session/store.go        # session in-memory (map + mutex)
│   └── handlers/handlers.go    # HTTP handler: /auth/login, /auth/callback, /api/me, /api/chat
├── go.mod
└── .env.example
```

## Cara menjalankan

### 1. Prasyarat

- Go 1.22+
- Sudah menyelesaikan Bagian 1–4 dokumentasi (App Registration Entra ID,
  Workforce Identity Pool, App Gemini Enterprise sudah aktif)

### 2. Tambahkan Redirect URI baru di Entra ID

App Registration `gemini-enterprise-sso-test` perlu Redirect URI **baru**
yang mengarah ke backend ini (bukan lagi ke `auth.cloud.google/...`):

1. Azure Portal → App Registration `gemini-enterprise-sso-test` → **Authentication**
2. Tambahkan Redirect URI platform **Web**:
   ```
   http://localhost:8080/auth/callback
   ```
   (Boleh dibiarkan berdampingan dengan Redirect URI lama.)

### 3. Setup environment

```bash
cp .env.example .env
# lalu edit .env, isi ENTRA_CLIENT_SECRET dengan client secret kamu
```

### 4. Install dependency & jalankan

```bash
go mod tidy
go run main.go
```

Server jalan di `http://localhost:8080`.

### 5. Test alurnya

Buka browser ke:

```
http://localhost:8080/auth/login
```

Login pakai akun Microsoft kamu → setelah sukses, browser diarahkan ke
`FRONTEND_URL` (default `http://localhost:5173` — boleh belum ada apa-apa
di situ dulu, yang penting proses login & cookie-nya berhasil).

Cek status login (dari browser yang sama, supaya cookie ikut terkirim):

```bash
curl -i http://localhost:8080/api/me --cookie-jar cookies.txt --cookie cookies.txt
```

Coba fitur chat (setelah login):

```bash
curl -X POST http://localhost:8080/api/chat \
  --cookie cookies.txt \
  -H "Content-Type: application/json" \
  -d '{"message": "Ringkas email terbaru saya"}'
```

## Batasan versi latihan ini (BUKAN untuk production)

Kode ini sengaja dibuat **sesederhana mungkin** untuk latihan/POC. Sebelum
dipakai di lingkungan produksi CIMB Niaga, minimal perbaiki hal berikut:

| Area | Kondisi sekarang | Perbaikan yang dibutuhkan |
|---|---|---|
| Verifikasi ID Token | Signature **tidak** diverifikasi (`DecodeIDTokenUnsafe`) | Verifikasi signature via JWKS Microsoft, memakai library seperti `github.com/coreos/go-oidc` atau MSAL Go |
| Session store | In-memory (`map` + mutex), hilang saat restart | Redis atau store terpusat, supaya bisa di-scale multi-instance |
| Refresh token | Tidak ada — user harus login ulang saat token Google habis (±1 jam) | Simpan refresh token, implementasi refresh otomatis |
| CSRF state cookie | Sudah ada validasi `state`, tapi minim hardening tambahan | Tambah PKCE untuk lapisan keamanan ekstra |
| Rate limiting & logging keamanan | Belum ada | Tambah middleware rate limit, audit log tiap login |
| streamAssist | Dipanggil non-streaming (baca semua body sekaligus) | Relay Server-Sent Events ke frontend untuk UX chat real-time |

## Referensi nilai konfigurasi (dari dokumentasi setup)

Nilai-nilai ini sudah ada di `.env.example`, hasil dari Bagian 1–4 dokumentasi:

- **Entra Tenant ID**: `19b6d7e2-017b-470b-9fb6-ddfeb48e6068`
- **Entra Client ID** (App Registration SSO): `ac708c4b-8590-406d-a113-bf75403754e9`
- **Workforce Pool ID**: `cngpt-entra-pool`
- **Workforce Provider ID**: `entra-oidc-provider`
- **Gemini Enterprise App ID**: `gemini-enterprise-1783673478762`

> Nilai-nilai di atas berasal dari lingkungan **sandbox** (domain
> majucepat.net + Azure Free Trial pribadi). Saat replikasi ke tenant
> produksi CIMB Niaga, ganti semua nilai ini sesuai App Registration dan
> Workforce Pool yang baru.

---

## Connector Authorization (Outlook/Office 365)

### ⚠️ Penting: Endpoint Private API

Setelah investigasi mendalam dan komunikasi dengan Google, kami mendapatkan konfirmasi bahwa:

**❌ Endpoint `acquireAndStoreRefreshToken` TIDAK TERSEDIA untuk public API**
- Method ini adalah **private/internal API** eksklusif untuk Gemini WebApp
- Semua percobaan untuk memanggil endpoint ini dari custom app akan menghasilkan **404 Not Found**
- Bukan masalah permissions, whitelist, atau preview - endpoint memang tidak diekspos

### ✅ Solusi Resmi dari Google

Ada 2 pendekatan yang direkomendasikan:

#### **Opsi 1: Service-Level Authorization (Recommended untuk Production)**

**Konsep:** Admin IT melakukan authorization di level organisasi/tenant (bukan per-user)

**Keuntungan:**
- ✅ User experience paling smooth (tidak perlu authorize connector)
- ✅ User cukup login SSO → langsung bisa query email/calendar
- ✅ Standard enterprise B2B integration pattern
- ✅ Scalable untuk ribuan users

**Setup Required:**
1. **Di Microsoft Entra ID:** Admin grant Domain-Wide Delegation untuk Service Account
2. **Di GCP Console:** Konfigurasi connector dengan Service Account credentials
3. **Di Custom App:** Tidak perlu kode connector authorization - langsung query Gemini API

**Flow:**
```
User login SSO → Token exchange → Query Gemini API → Backend otomatis akses Outlook
```

#### **Opsi 2: Manual Authorization via WebApp (Paling Simple untuk POC) ✅ VERIFIED WORKING**

**Konsep:** User authorize Outlook connector **sekali** di Gemini Enterprise WebApp

**Keuntungan:**
- ✅ Setup tercepat (tidak perlu konfigurasi admin)
- ✅ Authorization tersimpan permanen (diikat ke Workforce Identity `assertion.sub`)
- ✅ Bisa dipakai di semua aplikasi dengan Principal yang sama
- ✅ **VERIFIED:** Custom app dengan proper request format bisa akses connector data

**Cara:**
1. User login ke custom app (SSO sudah berhasil)
2. Instruksikan user: "Silakan authorize Outlook connector di https://gemini.google.com sekali saja"
3. User buka Gemini webapp → klik "Connect" pada Outlook connector → authorize
4. Kembali ke custom app → langsung bisa query email
5. Authorization valid permanen untuk user tersebut

**Flow:**
```
User authorize di Gemini WebApp (sekali) → Refresh token tersimpan di Google Vault
→ Custom app query dengan Workforce token + proper format → Backend deteksi user → Pakai refresh token
```

**⚠️ PENTING: Request Format Requirements**

Setelah authorization, custom app HARUS mengirim request dengan format yang benar:

```json
{
  "query": {
    "parts": [{"text": "Ringkas email terbaru saya"}]
  },
  "toolsSpec": {
    "vertexAiSearchSpec": {
      "dataStoreSpecs": [
        {"dataStore": "projects/.../dataStores/outlook-federated-connector_*_mail"},
        {"dataStore": "projects/.../dataStores/outlook-federated-connector_*_mail-attachment"},
        {"dataStore": "projects/.../dataStores/outlook-federated-connector_*_calendar"},
        {"dataStore": "projects/.../dataStores/outlook-federated-connector_*_contact"}
      ]
    }
  }
}
```

**Kenapa ini diperlukan:**
- "Blended search" default TIDAK auto-include third-party connectors
- Connector data stores harus dispesifikasikan eksplisit
- Kode sudah diupdate di `internal/gemini/gemini.go` untuk include format ini

**Setup di `.env`:**
```bash
# Tambahkan Outlook Connector ID
OUTLOOK_CONNECTOR_ID=outlook-federated-connector_1783678287149

# Atau format lengkap juga OK:
OUTLOOK_CONNECTOR_ID=collections/outlook-federated-connector_1783678287149/dataConnector
```

Kode akan otomatis extract base ID dan build ke-4 data store paths.

### 🎯 Rekomendasi

- **Untuk Production:** Gunakan **Opsi 1 (Service-Level Authorization)**
- **Untuk POC/Testing:** Gunakan **Opsi 2 (Manual Authorization)**

### 📚 Detail Lengkap

Untuk penjelasan lengkap tentang investigasi dan solusi connector authorization, lihat:
- [CONNECTOR_AUTH_SOLUTION.md](./CONNECTOR_AUTH_SOLUTION.md) - Investigasi lengkap dan final resolution dari Google
- [CONNECTOR_IMPLEMENTATION_COMPLETE.md](./CONNECTOR_IMPLEMENTATION_COMPLETE.md) - Implementasi teknis yang sudah dikerjakan

### 🔍 Key Learnings

**Yang Kami Pelajari:**
1. Tidak semua endpoint di network capture adalah public API
2. Google menerapkan Zero-Trust untuk OAuth pihak ketiga (security by design)
3. Enterprise integration berbeda dengan individual use case
4. Federated identity sangat powerful - authorization diikat ke Principal ID, bukan aplikasi

---
