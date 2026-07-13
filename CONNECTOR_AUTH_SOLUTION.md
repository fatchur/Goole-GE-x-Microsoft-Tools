# Connector Authorization Solution - Based on Investigation

---

## ⚠️ FINAL RESOLUTION FROM GOOGLE (CRITICAL UPDATE)

**Date:** 2026-07-12

### TL;DR - Endpoint `acquireAndStoreRefreshToken` TIDAK TERSEDIA untuk Public API

Setelah iterasi troubleshooting dan komunikasi langsung dengan Google, kami mendapat konfirmasi resmi:

**❌ Method `acquireAndStoreRefreshToken` adalah PRIVATE/INTERNAL API**
- Endpoint ini eksklusif untuk Control Plane internal Gemini WebApp
- TIDAK diekspos di Public REST API (`discoveryengine.googleapis.com`)
- Pemanggilan dari aplikasi custom akan SELALU mengembalikan **404 Not Found**
- Bukan masalah whitelist, preview, atau IAM permissions

### Mengapa Google Mendesainnya Seperti Ini?

**1. Zero-Trust OAuth Handling (Keamanan)**

Google menerapkan isolasi ketat untuk refresh token pihak ketiga (Microsoft Outlook):

```
┌─────────────────────────────────────────────────────────────┐
│ MICROSOFT REFRESH TOKEN = HIGHLY SENSITIVE                  │
│ (Akses penuh ke email/dokumen perusahaan user)              │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
        ┌───────────────────────────────────────┐
        │ Google Vault & Secret Isolation       │
        │ • Hanya WebApp resmi yang bisa handle │
        │ • Custom backend TIDAK BOLEH akses    │
        │ • Mencegah token interception         │
        └───────────────────────────────────────┘
```

**Alasan Keamanan:**
- **Google Vault Isolation:** Refresh token Microsoft disimpan di Vault internal Google yang terisolasi
- **Consent Boundary:** Proses OAuth hanya boleh terjadi di domain Google resmi (`*.gemini.google.com`, `*.cloud.google.com`)
- **Zero-Trust:** Aplikasi pihak ketiga (termasuk custom backend) TIDAK dipercaya untuk menangani token OAuth Microsoft

**2. API Surface Limitation**

Dokumentasi publik Google untuk Data Connector hanya menyediakan API untuk:
- ✅ `Create` connector configuration
- ✅ `Update` connector configuration
- ✅ `List` connectors
- ✅ `Delete` connectors

**TIDAK termasuk:**
- ❌ `acquireAndStoreRefreshToken` - Mengalirkan authorization code individual user
- ❌ `updateEngineUserData` - Set `authState: AUTHORIZED` manual
- ❌ Endpoint lain untuk per-user OAuth token handling

---

### ✅ SOLUSI RESMI DARI GOOGLE

#### **Solusi 1: Service-Level / Admin Consent (🌟 SANGAT DIREKOMENDASIKAN)**

**Konsep:** Authorization di level **tenant/organisasi**, bukan per-user

**Cara Kerja:**
```
┌──────────────────────────────────────────────────────────────┐
│ 1. Admin IT Setup (Sekali Saja)                             │
│    • Konfigurasi Domain-Wide Delegation di Microsoft        │
│    • Setup connector dengan Service Account di GCP          │
│    • Approve akses skala organisasi                         │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ 2. User Login ke Custom App                                 │
│    • SSO via Microsoft Entra ID                             │
│    • Token exchange ke Google Workforce Identity            │
│    • Dapat token ya29...                                    │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ 3. Query Gemini API Langsung                                │
│    • POST /streamAssist dengan Bearer ya29...               │
│    • Backend Gemini deteksi assertion.sub user              │
│    • OTOMATIS gunakan Service Account untuk akses Outlook   │
│    • User bisa query email tanpa authorize lagi             │
└──────────────────────────────────────────────────────────────┘
```

**Kelebihan:**
- ✅ UX paling smooth (user tidak perlu step tambahan)
- ✅ Secure (menggunakan service account standard)
- ✅ Standard enterprise B2B integration pattern
- ✅ Maintainable (tidak ada popup/iframe complexity)

**Implementasi di Custom App:**
```go
// ❌ TIDAK PERLU kode connector authorization
// ❌ TIDAK PERLU /auth/connector/authorize
// ❌ TIDAK PERLU /auth/connector/callback
// ❌ TIDAK PERLU acquireAndStoreRefreshToken

// ✅ CUKUP:
// 1. User login SSO → dapat Workforce token
// 2. Panggil Gemini API dengan token tersebut
// 3. Backend Gemini otomatis akses Outlook via Service Account
```

**Langkah Setup:**
1. **Di Microsoft Entra ID:**
   - Admin grant consent untuk Graph API permissions (Mail.Read, Calendars.Read, dll.)
   - Setup Domain-Wide Delegation untuk Service Account Google

2. **Di GCP Console:**
   - Konfigurasi connector dengan Service Account credentials
   - Assign IAM permissions: `roles/discoveryengine.editor`

3. **Di Custom App:**
   - Hapus semua kode connector authorization
   - Langsung query Gemini API setelah user login SSO

---

#### **Solusi 2: Embed/Popup WebApp Authorization (Per-User Consent)**

**Konsep:** User authorize di halaman resmi Google via popup/iframe

**Cara Kerja:**
```
┌──────────────────────────────────────────────────────────────┐
│ 1. User di Custom App                                       │
│    • Sudah login SSO                                        │
│    • Klik tombol "Connect Outlook"                          │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ 2. Popup Window Terbuka                                     │
│    • URL: https://gemini.google.com/connector/authorize     │
│    • User authorize di halaman RESMI Google                 │
│    • OAuth flow dihandle oleh Google WebApp                 │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ 3. Popup Tertutup Otomatis                                  │
│    • Custom app deteksi popup closed                        │
│    • Refresh connector status                               │
│    • User bisa query email                                  │
└──────────────────────────────────────────────────────────────┘
```

**Kelebihan:**
- ✅ Per-user consent (comply dengan strict privacy policy)
- ✅ Secure (OAuth di domain Google resmi)
- ✅ Satu flow dalam custom app (tidak keluar aplikasi)

**Kekurangan:**
- ⚠️ UX agak clunky (popup window management)
- ⚠️ Butuh state management antara popup dan main window

**Implementasi di Custom App:**
```javascript
// Frontend
function authorizeOutlookConnector() {
  const width = 600;
  const height = 700;
  const left = (screen.width - width) / 2;
  const top = (screen.height - height) / 2;

  // Buka popup ke halaman RESMI Gemini Enterprise
  const popup = window.open(
    'https://gemini.google.com/app/connector/authorize?connector_id=outlook-federated-connector_1783678287149',
    'OutlookAuthorization',
    `width=${width},height=${height},left=${left},top=${top}`
  );

  // Polling untuk deteksi popup tertutup
  const checkPopup = setInterval(async () => {
    if (popup.closed) {
      clearInterval(checkPopup);

      // Refresh status connector
      await checkConnectorStatus();

      // Lanjut query
      alert('Outlook connector authorized!');
    }
  }, 500);
}
```

**Catatan Penting:**
- URL exact popup bisa berbeda (perlu dicari URL resmi dari Gemini Enterprise)
- Atau, arahkan user untuk satu kali authorize di Gemini webapp secara manual

---

## 🔍 CRITICAL DISCOVERY: Request Format Requirement

### Problem yang Ditemukan

Setelah user authorize connector di Gemini WebApp, custom app tetap tidak bisa akses data connector meskipun menggunakan Workforce token yang sama.

**Root Cause:** Request format tidak lengkap!

### Network Capture Analysis

**Gemini WebApp Request (WORKING):**
```json
{
  "query": {
    "parts": [
      {"text": "Ringkas email terbaru saya"}
    ]
  },
  "toolsSpec": {
    "vertexAiSearchSpec": {
      "dataStoreSpecs": [
        {
          "dataStore": "projects/945912627556/locations/global/collections/default_collection/dataStores/outlook-federated-connector_1783678287149_mail"
        },
        {
          "dataStore": "projects/945912627556/locations/global/collections/default_collection/dataStores/outlook-federated-connector_1783678287149_mail-attachment"
        },
        {
          "dataStore": "projects/945912627556/locations/global/collections/default_collection/dataStores/outlook-federated-connector_1783678287149_calendar"
        },
        {
          "dataStore": "projects/945912627556/locations/global/collections/default_collection/dataStores/outlook-federated-connector_1783678287149_contact"
        }
      ]
    }
  }
}
```

**Custom App Request (NOT WORKING):**
```json
{
  "query": {
    "text": "Ringkas email terbaru saya"
  }
  // ❌ Missing toolsSpec!
  // ❌ Gemini doesn't know which data stores to query
}
```

### Key Findings

1. **"Blended Search" Does NOT Auto-Include Third-Party Connectors**
   - Default behavior hanya query Google-managed sources
   - Third-party connectors (Outlook) HARUS dispesifikasikan eksplisit
   - Tidak bisa rely pada "default" behavior

2. **Data Store Specs Must Be Explicit**
   - Harus list semua 4 data stores dari Outlook connector:
     - `outlook-federated-connector_*_mail`
     - `outlook-federated-connector_*_mail-attachment`
     - `outlook-federated-connector_*_calendar`
     - `outlook-federated-connector_*_contact`

3. **Query Format Changed**
   - Old: `query.text` (string)
   - New: `query.parts[].text` (array of objects)

### Solution Implementation

**Updated `internal/gemini/gemini.go`:**

```go
type queryRequest struct {
	Query struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"query"`
	ToolsSpec *struct {
		VertexAISearchSpec *struct {
			DataStoreSpecs []struct {
				DataStore string `json:"dataStore"`
			} `json:"dataStoreSpecs"`
		} `json:"vertexAiSearchSpec,omitempty"`
	} `json:"toolsSpec,omitempty"`
}

func (c *Client) Ask(googleAccessToken, questionText string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s/assistants/default_assistant:streamAssist",
		c.baseURL(), c.cfg.GCPProjectID, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	var reqBody queryRequest
	reqBody.Query.Parts = []struct {
		Text string `json:"text"`
	}{
		{Text: questionText},
	}

	// Add toolsSpec to explicitly include Outlook connector data stores
	if c.cfg.OutlookConnectorID != "" {
		// Extract base connector ID (e.g., "outlook-federated-connector_1783678287149")
		baseID := extractConnectorBaseID(c.cfg.OutlookConnectorID)

		reqBody.ToolsSpec = &struct {
			VertexAISearchSpec *struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			} `json:"vertexAiSearchSpec,omitempty"`
		}{
			VertexAISearchSpec: &struct {
				DataStoreSpecs []struct {
					DataStore string `json:"dataStore"`
				} `json:"dataStoreSpecs"`
			}{
				DataStoreSpecs: []struct {
					DataStore string `json:"dataStore"`
				}{
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail", c.cfg.GCPProjectID, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_mail-attachment", c.cfg.GCPProjectID, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_calendar", c.cfg.GCPProjectID, baseID)},
					{DataStore: fmt.Sprintf("projects/%s/locations/global/collections/default_collection/dataStores/%s_contact", c.cfg.GCPProjectID, baseID)},
				},
			},
		}
	}

	// ... rest of the code
}
```

### Verification: Manual Authorization WORKS!

**Complete Flow:**

```
1. User authorize connector di Gemini WebApp (one-time)
   ↓
2. Refresh token tersimpan di Google Vault
   Tied to: Workforce Principal (principal://iam.googleapis.com/.../subject/abc-123)
   ↓
3. Custom app login dengan same Microsoft account
   ↓
4. Token exchange → same Workforce Principal
   ↓
5. Custom app query Gemini API dengan:
   ✅ Workforce token (same Principal)
   ✅ Proper request format (include dataStoreSpecs)
   ↓
6. Gemini API:
   ✅ Recognize Principal
   ✅ Fetch refresh token dari Vault (tied to Principal)
   ✅ Query Outlook dengan refresh token
   ✅ Return results with email/calendar data! ✅
```

**Confirmation:**
- ✅ Authorization IS shared across apps (via Workforce Principal)
- ✅ No need to re-authorize in custom app
- ✅ Only need correct request format
- ✅ Manual authorization via WebApp is SUFFICIENT

---

### 🎯 REKOMENDASI FINAL (UPDATED)

**Untuk POC/Development:**
→ Gunakan **Solusi 2 (Manual Authorization via WebApp)** + **Proper Request Format**

**Steps:**
1. User login ke custom app via SSO
2. Instruksikan user: "Please authorize Outlook connector at https://gemini.google.com once"
3. User buka Gemini WebApp, authorize connector (one-time)
4. Kembali ke custom app
5. Custom app query dengan proper request format (include dataStoreSpecs)
6. ✅ Connector data accessible!

**Untuk Production Enterprise App:**
→ Gunakan **Solusi 1 (Service-Level / Admin Consent)**

**Alasan:**
1. Paling seamless untuk end-user (no manual authorization step)
2. Standard enterprise integration pattern
3. Lebih secure (domain-wide delegation)
4. Lebih maintainable (no popup complexity)
5. Scalable untuk ribuan users

**Untuk Prototype / POC:**
→ Gunakan **Manual Authorization** (paling simple)

**Cara:**
1. User login ke custom app (SSO)
2. **Instruksikan user:** "Please authorize Outlook connector at https://gemini.google.com once"
3. User buka Gemini webapp → authorize Outlook (sekali saja)
4. Kembali ke custom app → langsung bisa query
5. Authorization tersimpan permanent (diikat ke `assertion.sub`)

---

### 📊 Status Final Implementasi

| Komponen | Status | Keterangan |
|----------|--------|------------|
| ✅ Microsoft SSO | **WORKING** | Login via Entra ID berhasil |
| ✅ Workforce Identity Token Exchange | **WORKING** | STS exchange dengan `assertion.sub` berhasil |
| ❌ `acquireAndStoreRefreshToken` endpoint | **TIDAK TERSEDIA** | Private API, tidak untuk public use |
| ✅ Gemini Query API | **READY TO USE** | Langsung panggil dengan token `ya29...` |
| 🔄 Connector Authorization | **USE WEBAPP** | User authorize sekali di Gemini webapp, atau implement Admin Consent |

---

### 📚 Lessons Learned

**Apa yang Kami Pelajari:**

1. **Tidak semua endpoint di network capture adalah public API**
   - WebApp internal bisa menggunakan private endpoints
   - Dokumentasi public API adalah source of truth

2. **Google menerapkan Zero-Trust untuk OAuth pihak ketiga**
   - Security by design: isolasi refresh token di Google Vault
   - Custom apps tidak dipercaya untuk handle OAuth tokens

3. **Enterprise integration berbeda dengan individual use case**
   - Service-level authorization (admin consent) adalah pattern yang benar
   - Per-user OAuth delegation bukan untuk custom enterprise apps

4. **Federated identity sangat powerful**
   - User authorize sekali di platform manapun (webapp/custom app)
   - Authorization diikat ke Workforce Identity (`assertion.sub`)
   - Token bisa dipakai di semua aplikasi dengan Principal yang sama

---

## Discovery Summary (Historical Investigation)

From network capture, we found the connector authorization flow:

### Flow Diagram:
```
1. Click "Authorize"
   → GET vertexaisearch.cloud.google.com/oauth-redirect?continue_uri=...

2. Redirect to Microsoft OAuth
   → login.microsoftonline.com/.../authorize
   → client_id: f2e7e1f8-9815-4e5d-ab94-4e1a16727041 (Gemini's App Registration)
   → redirect_uri: vertexaisearch.cloud.google.com/oauth-redirect
   → scope: https://graph.microsoft.com/.default+offline_access

3. User authorizes → Redirect back

4. POST request to update user data:
   {
     "engineUserData": {
       "connectorAuthStates": [{
         "dataConnector": "collections/outlook-federated-connector_1783678287149/dataConnector",
         "authState": "AUTHORIZED"
       }]
     }
   }
```

## Critical Findings

### Problem 1: Different Microsoft App Registration
- Gemini uses: `f2e7e1f8-9815-4e5d-ab94-4e1a16727041`
- Our app uses: `ac708c4b-8590-406d-a113-bf75403754e9`
- Connector authorization tied to specific App Registration
- **This explains the authorization mismatch!**

### Problem 2: Google-Managed Redirect
- `redirect_uri`: `https://vertexaisearch.cloud.google.com/oauth-redirect`
- We cannot intercept or customize this
- Authorization tokens go to Google's server, not ours

### Key Discovery: Update User Data API
- There's an API to set `connectorAuthStates`
- Sets `authState` to `"AUTHORIZED"` for specific connector
- Includes `userPseudoId` to identify user

## Potential Solutions

### Solution 1: Find & Call the Update API ⭐ (Most Promising)

**If we can find the exact API endpoint:**

```
POST /v1alpha/projects/{project}/locations/{location}/engineUserData
or
POST /v1alpha/projects/{project}/locations/{location}/users/{userId}
or similar...

Body:
{
  "engineUserData": {
    "userPseudoId": "{userId}",
    "connectorAuthStates": [{
      "dataConnector": "collections/outlook-federated-connector_1783678287149/dataConnector",
      "authState": "AUTHORIZED"
    }]
  }
}
```

**Questions:**
1. What is the exact API endpoint URL?
2. Can we call it with our Workforce Identity token?
3. What is the `userPseudoId`? How to get it for our user?

**Testing Steps:**
```bash
# Try to call the API
curl -X POST \
  "https://discoveryengine.googleapis.com/v1alpha/projects/945912627556/locations/global/..." \
  -H "Authorization: Bearer $WORKFORCE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "engineUserData": {
      "userPseudoId": "...",
      "connectorAuthStates": [{
        "dataConnector": "collections/outlook-federated-connector_1783678287149/dataConnector",
        "authState": "AUTHORIZED"
      }]
    }
  }'
```

**If this works:**
✅ We can set authorization status programmatically!
✅ Users don't need separate Google OAuth!
✅ Works with Workforce Identity!

**If this fails:**
❌ API probably validates actual OAuth tokens
❌ Cannot fake authorization
❌ Need real OAuth flow

### Solution 2: Use Gemini's App Registration

**Problem:** We don't have access to Gemini's App Registration client secret.

**Idea:** Can we trigger OAuth flow using Gemini's `client_id` but with our own redirect handling?

**Unlikely to work because:**
- redirect_uri must match registered URI
- Google's redirect_uri is hardcoded
- We cannot intercept tokens

### Solution 3: Register Our Connector with Our App Registration

**Concept:**
1. Use our App Registration: `ac708c4b-8590-406d-a113-bf75403754e9`
2. Register connector yang points to our App Registration
3. Users authorize dengan our app's OAuth
4. Connector uses our authorization

**Questions:**
- How to register connector programmatically?
- Is there API for connector registration?
- Would this even work with Gemini Enterprise?

**Likely answer:** Not possible - connectors are admin-configured in GCP Console

### Solution 4: Replicate Google's OAuth Proxy Flow

**Concept:**
Create our own version of `vertexaisearch.cloud.google.com/oauth-redirect`:

```
1. Our backend endpoint: /auth/connector/authorize
2. Generate Microsoft OAuth URL with our App Registration
3. Redirect user to Microsoft
4. Our callback: /auth/connector/callback
5. After successful OAuth, call Update User Data API
```

**Implementation:**
```go
// Backend: /auth/connector/authorize
func (h *Handler) ConnectorAuthorize(c *fiber.Ctx) error {
    state := generateState()

    // Microsoft OAuth URL with OUR App Registration
    authURL := fmt.Sprintf(
        "https://login.microsoftonline.com/%s/oauth2/v2.0/authorize?"+
        "client_id=%s&"+
        "redirect_uri=%s&"+
        "response_type=code&"+
        "scope=https://graph.microsoft.com/.default+offline_access&"+
        "state=%s",
        tenantID,
        OUR_CLIENT_ID,  // ac708c4b-8590-406d-a113-bf75403754e9
        OUR_REDIRECT_URI,  // http://localhost:8080/auth/connector/callback
        state,
    )

    return c.Redirect(authURL)
}

// Backend: /auth/connector/callback
func (h *Handler) ConnectorCallback(c *fiber.Ctx) error {
    code := c.Query("code")

    // Exchange code for token
    token := exchangeCode(code)

    // Call Update User Data API
    err := updateConnectorAuthState(
        sess.GoogleAccessToken,  // Use Workforce token to call API
        userPseudoId,
        "AUTHORIZED",
    )

    return c.SendString("Authorization successful!")
}
```

**Challenge:**
- Still need to find Update User Data API endpoint
- Need to get `userPseudoId` for our user
- API might reject if OAuth token from different App Registration

### Solution 5: Hybrid Auth (Fallback Plan)

If all above fail, implement Google OAuth popup as planned in `HYBRID_AUTH_PLAN.md`.

## Next Investigation Steps

### Priority 1: Find API Endpoint 🔍

**From network capture, find:**

1. **Exact URL** of POST request with `connectorAuthStates` body
   ```
   Look for request containing:
   "authState": "AUTHORIZED"
   ```

2. **Request Headers**
   ```
   - Authorization: Bearer ...
   - X-Goog-User-Project: ...
   - Content-Type: application/json
   - Any other custom headers
   ```

3. **Full Request URL**
   ```
   Example pattern to look for:
   - /v1alpha/projects/{project}/locations/{location}/engineUserData
   - /v1alpha/projects/{project}/locations/{location}/users/{userId}
   - /v1alpha/.../connectors/.../authorize
   ```

### Priority 2: Get User Pseudo ID 🆔

**In network capture, find how to get:**
```
"userPseudoId": "a4b847bc7b594ddeb19014ee0173b114"
```

**Look for API calls that return this:**
- GET /v1alpha/.../users/me
- GET /v1alpha/.../engineUserData
- Or it might be in session info

### Priority 3: Test API Call 🧪

Once we have endpoint and userPseudoId, test:

```bash
curl -X POST \
  "{DISCOVERED_API_URL}" \
  -H "Authorization: Bearer ${WORKFORCE_IDENTITY_TOKEN}" \
  -H "Content-Type: application/json" \
  -H "x-goog-user-project: 945912627556" \
  -d '{
    "engineUserData": {
      "userPseudoId": "${USER_PSEUDO_ID}",
      "connectorAuthStates": [{
        "dataConnector": "collections/outlook-federated-connector_1783678287149/dataConnector",
        "authState": "AUTHORIZED"
      }]
    }
  }'
```

**Expected outcomes:**

✅ **Success (200 OK):**
- Connector authorized programmatically!
- Can query email without additional OAuth!
- Solution works with Workforce Identity!

❌ **Unauthorized (401/403):**
- API validates actual OAuth tokens
- Cannot set authorization without real OAuth flow
- Need to implement hybrid auth

## Recommended Next Actions

1. **Immediate:** Get exact API endpoint URL from network capture
2. **Next:** Find how to get `userPseudoId` for our users
3. **Then:** Test API call with our Workforce Identity token
4. **Finally:** Implement based on test results

## Expected Timeline

- **If API call works:** 2-3 hours to implement
- **If API call fails:** Proceed with hybrid auth (6-8 hours)

---

**Key Question to Answer:**

**What is the exact URL of the POST request that sets `authState: "AUTHORIZED"`?**

This is the most critical piece of information needed to proceed.
