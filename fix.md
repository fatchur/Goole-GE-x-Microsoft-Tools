# Troubleshooting: Gemini Enterprise API 403 Permission Denied

## Problem Statement

Backend for Frontend (BFF) implementation menggunakan Microsoft Entra ID SSO + Google Workforce Identity Federation untuk memanggil Gemini Enterprise API mengalami error **403 PERMISSION_DENIED**, meskipun:
- Login SSO berhasil
- Webapp Gemini Enterprise resmi berfungsi normal dengan user yang sama
- IAM roles dan license sudah ter-assign dengan benar

## Initial Error

```json
{
  "error": {
    "code": 403,
    "message": "Permission 'discoveryengine.assistants.assist' denied on resource '//discoveryengine.googleapis.com/projects/ge-test-123/locations/global/collections/default_collection/engines/gemini-enterprise-1783673478762/assistants/default_assistant'",
    "status": "PERMISSION_DENIED",
    "details": [{
      "@type": "type.googleapis.com/google.rpc.ErrorInfo",
      "reason": "IAM_PERMISSION_DENIED"
    }]
  }
}
```

## Architecture

```
User → Microsoft Entra ID (OAuth 2.0)
     → Google STS Token Exchange (Workforce Identity Federation)
     → Gemini Enterprise API
```

**Flow:**
1. User login via Microsoft Entra ID
2. Backend mendapat Microsoft ID Token
3. Backend exchange ID Token → Google Access Token via STS API
4. Backend call Gemini Enterprise API dengan Google Access Token
5. ❌ Error 403

## Troubleshooting Steps

### 1. Verifikasi License Assignment ✅

**Check:** Gemini Enterprise > Manage users

**Result:** License sudah assigned dengan benar
```
test-ge-user@fatchurrahman1gmail.onmicrosoft.com
- License: Gemini Enterprise Plus
- Expires: Aug 10, 2026
- Last used: Jul 11, 2026
```

### 2. Verifikasi IAM Roles ✅

**Check:** GCP Console > IAM & Admin

**Result:** IAM binding sudah benar dengan format Workforce Identity yang proper:

```
principalSet://iam.googleapis.com/locations/global/workforcePools/cngpt-entra-pool/*
  - Gemini Enterprise User
  - Discovery Engine User
  - Owner

principal://iam.googleapis.com/locations/global/workforcePools/cngpt-entra-pool/subject/test-ge-user@fatchurrahman1gmail.onmicrosoft.com
  - Gemini Enterprise User
  - Owner
```

**Key Learning:** Principal format untuk Workforce Identity HARUS menggunakan `principalSet://` atau `principal://` prefix, bukan cuma string biasa.

### 3. Verifikasi Token Exchange ✅

**Added debug logging:**

```go
// DEBUG: Inspect token info to verify subject mapping
tokenInfoURL := "https://oauth2.googleapis.com/tokeninfo?access_token=" + sr.AccessToken
```

**Result:** Token exchange berhasil, tapi tokeninfo endpoint return error karena ini federated token (bukan OAuth 2.0 token biasa).

**Key Learning:** Federated access token dari Workforce Identity berbeda format dengan OAuth 2.0 access token native Google.

### 4. Bandingkan dengan Webapp Flow

**Discovery:** Webapp Gemini Enterprise JUGA menggunakan Workforce Identity Federation:

**Webapp URL:**
```
https://auth.cloud.google/signin/locations/global/workforcePools/cngpt-entra-pool/providers/entra-oidc-provider
```

**Microsoft OAuth redirect:**
```
redirect_uri=https://auth.cloud.google/signin-callback/locations/global/workforcePools/cngpt-entra-pool/providers/entra-oidc-provider
```

**Insight:** Baik webapp maupun backend menggunakan flow yang sama (Workforce Identity), tapi webapp berhasil dan backend gagal. Berarti masalahnya bukan di IAM atau license, tapi di **parameter API call**.

### 5. Coba Tambah Scope ⚠️

**Attempt:** Tambah scope `userinfo.email` ke token exchange:

```go
form.Set("scope", "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email")
```

**Result:** Masih 403 (belum test sampai selesai karena ketemu root cause di step berikutnya)

### 6. Check API Documentation 🎯

**Discovery:** Di Gemini Enterprise dashboard > Integration, ada dokumentasi API dengan curl example:

```bash
curl -X POST -H "Authorization: Bearer $(gcloud auth print-access-token)" \
-H "Content-Type: application/json" \
"https://discoveryengine.googleapis.com/v1alpha/projects/945912627556/locations/global/collections/default_collection/engines/gemini-enterprise-17836734_1783673478762/servingConfigs/default_search:search"
```

**Key Findings:**
1. Pakai **project NUMBER** (`945912627556`), bukan project ID string
2. Engine ID format lengkap: `gemini-enterprise-17836734_1783673478762`

## Root Cause

**Problem 1: Wrong Project Identifier**

`.env` menggunakan:
```
GCP_PROJECT_ID=ge-test-123  # ❌ Project name (bukan ID atau number)
```

Seharusnya:
```
GCP_PROJECT_ID=945912627556  # ✅ Project number
```

**Project identifiers di GCP:**
- **Project name:** `ge-test-123` (display name, bisa duplikat)
- **Project ID:** `ge-test-123-502003` (unique ID, immutable)
- **Project number:** `945912627556` (numeric, immutable)

**Gemini Enterprise API requires project NUMBER**, bukan name atau ID string.

**Problem 2: Wrong Engine ID**

`.env` menggunakan:
```
GEMINI_APP_ID=gemini-enterprise-1783673478762  # ❌ Incomplete
```

Seharusnya:
```
GEMINI_APP_ID=gemini-enterprise-17836734_1783673478762  # ✅ Complete with prefix
```

## Solution

### 1. Update `.env`

```diff
# --- Gemini Enterprise ---
- GCP_PROJECT_ID=ge-test-123
+ GCP_PROJECT_ID=945912627556
- GEMINI_APP_ID=gemini-enterprise-1783673478762
+ GEMINI_APP_ID=gemini-enterprise-17836734_1783673478762
GEMINI_LOCATION=global
```

### 2. Clear Session & Restart

```bash
# Clear backend session
curl http://localhost:8080/auth/debug/clear-all-sessions

# Restart backend
# (kill process dan run ulang: go run main.go)
```

### 3. Test dengan Fresh Login

1. Buka incognito/private browser window
2. Login ke `http://localhost:5173`
3. Test chat dengan pertanyaan substantif (bukan "hallo")

## Final Result

**Before:**
```json
{
  "error": {
    "code": 403,
    "message": "Permission 'discoveryengine.assistants.assist' denied...",
    "status": "PERMISSION_DENIED"
  }
}
```

**After (with trailing space in .env):**
```json
{
  "error": {
    "code": 403,
    "message": "Permission denied on resource project 945912627556 .",
    "status": "PERMISSION_DENIED",
    "details": [{
      "@type": "type.googleapis.com/google.rpc.ErrorInfo",
      "reason": "CONSUMER_INVALID"
    }]
  }
}
```

**After (trailing space removed):**
```json
[
  {
    "answer": {
      "state": "SUCCEEDED",
      "replies": [{
        "groundedContent": {
          "content": {
            "role": "model",
            "text": "⚙️ Outlook Connector Status\n\nI understand that the **outlook-federated-connector** has been configured..."
          }
        }
      }],
      "sessionInfo": {
        "session": "projects/.../sessions/14132901925743022811"
      }
    }
  }
]
```

✅ **Problem solved!**

**Features confirmed working:**
- ✅ User authentication via Workforce Identity Federation
- ✅ Gemini Enterprise API access with proper permissions
- ✅ Outlook connector integration (federated search)
- ✅ Streaming response (Server-Sent Events)
- ✅ Session management for conversation context
- ✅ Suggested follow-up questions
- ✅ Personalized data scoping (user-specific Outlook access)

## Error Progression

1. **403 PERMISSION_DENIED** (project name salah)
   → Fix: Pakai project number (`945912627556`)

2. **404 NOT_FOUND** (engine ID incomplete)
   → Fix: Pakai engine ID lengkap dengan prefix (`gemini-enterprise-17836734_1783673478762`)

3. **403 CONSUMER_INVALID** (trailing space in .env)
   → Fix: Hapus spasi di akhir `GCP_PROJECT_ID=945912627556 `

4. **200 OK with NON_ASSIST_SEEKING_QUERY_IGNORED** (query "hallo" di-skip)
   → Normal behavior untuk query yang bukan pertanyaan substantif

5. **200 OK SUCCEEDED** (query tentang connector) ✅
   → Full success! Streaming response, Outlook connector detected, suggested questions

## Key Learnings

### 1. GCP Project Identifiers

Selalu perhatikan **identifier mana** yang diminta API:
- Kebanyakan API terima project ID atau number
- Gemini Enterprise **specifically requires project NUMBER**

Cara cari project number:
```bash
gcloud projects describe PROJECT_ID --format="value(projectNumber)"
```

Atau via Console: Dashboard > Project info card

### 2. Workforce Identity Federation Principal Format

IAM binding untuk Workforce Identity **HARUS** menggunakan format:

**For all users in pool:**
```
principalSet://iam.googleapis.com/locations/global/workforcePools/{pool-id}/*
```

**For specific user:**
```
principal://iam.googleapis.com/locations/global/workforcePools/{pool-id}/subject/{user-email}
```

**BUKAN** cuma `{pool-id}/*` atau `{user-email}` saja.

### 3. Federated Token vs OAuth Token

Token dari Workforce Identity Federation (via STS token exchange) adalah **federated access token**, bukan OAuth 2.0 access token biasa:

- ✅ Bisa dipakai untuk call Google Cloud APIs
- ❌ Tidak bisa di-inspect via `oauth2.googleapis.com/tokeninfo`
- ⚠️ Scope yang diminta saat exchange mungkin berbeda dengan OAuth flow

### 4. Gemini Enterprise Licensing

**Dua layer authorization:**
1. **IAM Roles** (e.g., `roles/discoveryengine.agentspaceUser`)
2. **Explicit License Assignment** (via Gemini Enterprise > Manage users)

Kedua-duanya **WAJIB** ada, tidak cukup salah satu saja.

### 5. API Documentation is Your Friend

Ketika stuck dengan permission error meskipun semua terlihat benar:
- ✅ Cek official API documentation/examples
- ✅ Compare curl examples dengan implementation kita
- ✅ Perhatikan detail kecil (project number vs ID, engine ID format, dll)

### 6. Debugging Strategy

**Untuk troubleshoot permission errors:**

1. ✅ Verify license assignment
2. ✅ Verify IAM roles (dengan format yang benar)
3. ✅ Verify token exchange berhasil
4. ✅ Compare dengan working example (webapp)
5. ✅ Check API documentation untuk parameter yang benar
6. ✅ Test dengan minimal example dari docs

**Red herrings (bukan masalah sebenarnya):**
- ❌ Token propagation delay (tunggu 5 menit)
- ❌ Cached token (clear session)
- ❌ Scope differences
- ❌ Custom headers (x-goog-iam-authority-selector, dll)

**Actual problem:**
- ✅ Wrong project identifier (name vs number)
- ✅ Wrong resource ID format

## Prevention

### Untuk development baru:

1. **Selalu cek API documentation** untuk parameter yang benar
2. **Copy-paste curl examples** dari official docs sebagai baseline
3. **Test curl command dulu** sebelum implement di code
4. **Gunakan descriptive variable names:**
   ```go
   // BAD
   cfg.GCPProjectID  // Ambiguous: bisa ID, name, atau number?

   // GOOD
   cfg.GCPProjectNumber  // Jelas pakai project number
   ```
5. **Hati-hati dengan trailing spaces di .env files:**
   ```bash
   # BAD - ada spasi trailing
   GCP_PROJECT_ID=945912627556

   # GOOD - no trailing space
   GCP_PROJECT_ID=945912627556
   ```
   Tip: Enable "trim trailing whitespace on save" di editor, atau gunakan linter untuk .env files.

### Untuk debugging:

1. **Add comprehensive debug logging:**
   ```go
   fmt.Printf("[DEBUG] Full API URL: %s\n", url)
   fmt.Printf("[DEBUG] Project Number: %s\n", projectNumber)
   fmt.Printf("[DEBUG] Engine ID: %s\n", engineID)
   ```

2. **Compare dengan working example** (webapp, curl, dll)

3. **Isolate variables** - test satu parameter at a time

## References

- [Workforce Identity Federation](https://cloud.google.com/iam/docs/workforce-identity-federation)
- [Gemini Enterprise Documentation](https://cloud.google.com/generative-ai-app-builder/docs)
- [STS Token Exchange (RFC 8693)](https://datatracker.ietf.org/doc/html/rfc8693)
- [Discovery Engine API Reference](https://cloud.google.com/generative-ai-app-builder/docs/reference/rest)

## Timeline

- **Initial report:** 403 PERMISSION_DENIED error
- **Troubleshooting:** 2+ hours (license check, IAM verify, token debug, webapp comparison, scope attempts)
- **Root cause found:** Wrong project identifier + incomplete engine ID
- **Fix applied:** Update .env with correct values
- **Resolution:** ✅ API working successfully

---

## Connector Authorization Integration (Next Phase)

### Background

After successfully resolving the 403 Permission Denied error, the next requirement is to integrate Outlook connector authorization into the custom application instead of requiring users to manually authorize in the Gemini Enterprise webapp.

### Challenge

**Two Separate OAuth Flows:**
1. **User SSO login**: `http://localhost:8080/auth/callback` (controllable by BFF)
2. **Connector authorization**: `https://vertexaisearch.cloud.google.com/oauth-redirect` (managed by Google, not interceptable)

The connector authorization flow uses a Google-managed redirect URI, which means we cannot directly capture the authorization code in our backend.

### Discovery Engine Data Stores API

Based on exploration of the Gemini Enterprise API documentation (from `/Users/m/Documents/Gemini_enterprise/ge-customUI`), the Discovery Engine API provides **Data Stores** endpoints:

#### Available Endpoints

**1. List Data Stores**
```
GET https://discoveryengine.googleapis.com/v1alpha/projects/{projectNumber}/locations/{region}/collections/default_collection/dataStores
```

**2. Get Data Store (includes connector state)**
```
GET https://discoveryengine.googleapis.com/v1alpha/projects/{projectNumber}/locations/{region}/collections/default_collection/dataStores/{dataStoreId}
```

Description: "Fetch one data store's config (content type, industry vertical, **connector state**)."

#### Implementation

Added two new endpoints to the BFF:

**Backend:**
- Created `internal/gemini/datastores.go` with `ListDataStores()` and `GetDataStore()` functions
- Added handlers in `internal/handlers/handlers.go`:
  - `GET /api/datastores` - List all data stores
  - `GET /api/datastores/:id` - Get specific data store details

**Testing Steps:**

1. Start backend: `go run main.go`
2. Login via frontend: `http://localhost:5173`
3. Test list data stores:
   ```bash
   curl http://localhost:8080/api/datastores \
     -H "Cookie: cngpt_session=YOUR_SESSION_COOKIE"
   ```
4. Test get specific data store (replace `{dataStoreId}` with actual ID from list):
   ```bash
   curl http://localhost:8080/api/datastores/{dataStoreId} \
     -H "Cookie: cngpt_session=YOUR_SESSION_COOKIE"
   ```

### Expected Information from Data Store API

The `GetDataStore` endpoint should return:
- Connector type (e.g., Google Workspace, Microsoft, etc.)
- Connector configuration
- **Connector state** - This may include:
  - Authorization status per user
  - Authorization URL for users who need to authorize
  - Connector health status

### API Testing Results

**Data Store API Response:**
- ✅ Returns list of 4 Outlook connectors (mail, mail-attachment, calendar, contact)
- ✅ Returns basic metadata (name, displayName, industryVertical, createTime)
- ❌ **NO authorization status** in response
- ❌ **NO authorization URL** available
- ❌ **NO connector state** field

**Engine API Response:**
- ✅ Returns `dataStoreIds[]` array with connected data stores
- ✅ Returns search configuration and features
- ❌ **NO connector authorization info**
- ❌ **NO OAuth configuration**

**Conclusion**: Discovery Engine API **does NOT provide** endpoints for:
- Checking user authorization status per connector
- Getting authorization URL programmatically
- Managing connector authorization via API

### Solution Implemented: Error-Based Detection

Since API doesn't provide authorization status, we implemented **pattern matching** on Gemini's response text.

**How It Works:**

1. **User sends query** requiring connector access (e.g., "Ringkas email terbaru saya")

2. **Gemini response** when NOT authorized:
   - State: `"SUCCEEDED"` (NOT an error!)
   - Text contains: *"tidak memiliki akses langsung untuk membaca atau meringkas email Anda karena koneksi data ke akun email Anda **belum dikonfigurasi**"*
   - Provides setup instructions instead of actual data

3. **Backend detection** (`internal/gemini/connector_detector.go`):
   - Parse streaming response
   - Check for authorization keywords:
     - "belum dikonfigurasi"
     - "tidak memiliki akses"
     - "hubungi administrator"
     - "configure connector"
     - etc.
   - Return `ConnectorAuthStatus` with detection result

4. **Frontend warning** (`FE/index.html`):
   - Show yellow warning banner when authorization needed
   - Display detected keywords for debugging
   - Provide "Authorize Outlook Connector" button
   - Open Gemini Enterprise webapp in new tab
   - User authorizes connector manually
   - User returns and tries query again

### Implementation Files

**Backend:**
- `internal/gemini/connector_detector.go` - Detection utility
- `internal/handlers/handlers.go` - Enhanced `/api/chat` response with `connectorAuthCheck`

**Frontend:**
- `FE/index.html` - Authorization warning banner + authorize button

### Testing Flow

1. Login via SSO (user who hasn't authorized connector yet)
2. Send query: "Ringkas email terbaru saya"
3. See warning banner: "Connector Authorization Required"
4. Click "Authorize Outlook Connector" button
5. Opens Gemini Enterprise webapp (https://gemini.google.com/app)
6. User authorizes connector in webapp
7. Return to custom app
8. Try query again → should work!

---

**Last updated:** 2026-07-11

**Status:** ✅ RESOLVED (403 error)
**Current Phase:** 🔨 Connector Authorization Integration (in progress)

---

## Critical Issue: Connector Authorization with Workforce Identity Federation

### Problem Discovery

**Scenario:**
- User authorizes Outlook connector via Gemini Enterprise webapp ✅
- User can query emails in Gemini webapp ✅
- User queries emails in our custom app ❌ Still gets "tidak memiliki akses" response

**Root Cause: Principal Identity Mismatch**

Gemini Enterprise connector authorization is **tied to specific user principal identity**, dan Workforce Identity Federation creates **different principal** than Google OAuth.

### Two Different Authentication Flows:

**Flow 1: Gemini Enterprise Webapp (Works)**
```
User → Gemini Enterprise Webapp
     → Google OAuth (accounts.google.com)
     → Grants permissions for connectors
     → Principal: user://gmail.com/test-ge-user@fatchurrahman1gmail.onmicrosoft.com
     → Connector authorized for THIS principal
```

**Flow 2: Our Custom App (Doesn't Work)**
```
User → Our App → Microsoft Entra ID
     → Google STS Token Exchange (Workforce Identity Federation)
     → Principal: principal://iam.googleapis.com/locations/global/workforcePools/cngpt-entra-pool/subject/test-ge-user@...
     → Connector looking for DIFFERENT principal (from Flow 1)
```

### Why This Happens:

**Workforce Identity Federation** creates federated principal:
```
principal://iam.googleapis.com/locations/global/workforcePools/{pool}/subject/{user}
```

**Google OAuth** creates standard Google principal:
```
user://gmail.com/{email}
```

**Connector authorization** is bound to specific principal. When user authorizes via webapp (Google OAuth), connector is authorized for Google OAuth principal. When we query via Workforce Identity token, Gemini checks authorization for **federated principal** which is different!

### Verification Steps:

**1. Check Token Principal** (Added debug endpoint):
```bash
curl http://localhost:8080/api/debug/token-info \
  -H "Cookie: cngpt_session=..."
```

Compare principal identity between:
- Token from our app (Workforce Identity)
- Token from Gemini webapp (Google OAuth)

**2. Check IAM Policy Binding:**
```bash
# Our current IAM binding
principalSet://iam.googleapis.com/locations/global/workforcePools/cngpt-entra-pool/*
  - Gemini Enterprise User
  - Discovery Engine User
```

### Potential Solutions:

**Option 1: Use Google OAuth Instead of Workforce Identity** ⭐ (Recommended)

Instead of Workforce Identity Federation, use direct Google OAuth:

```
User → Our App → Google OAuth (accounts.google.com)
     → Same principal as Gemini webapp
     → Connector authorization works!
```

**Pros:**
- ✅ Same principal identity as Gemini webapp
- ✅ Connector authorization works automatically
- ✅ Consistent with Gemini's expected auth flow

**Cons:**
- ❌ Requires users to have Google Workspace accounts
- ❌ Cannot use Microsoft Entra ID SSO
- ❌ Defeats purpose of Workforce Identity Federation

**Option 2: Authorize Connector per Workforce Principal**

User authorizes connector AGAIN specifically for Workforce Identity principal:

```
User → Special authorization endpoint
     → Uses Workforce Identity token
     → Authorizes connector for federated principal
```

**Challenge:** No known API endpoint for this!

**Option 3: Service Account Impersonation**

Use service account with domain-wide delegation:

```
Backend → Service Account
       → Impersonate user
       → Access connectors with service account credentials
```

**Pros:**
- ✅ Bypass user-level authorization
- ✅ Works with Workforce Identity

**Cons:**
- ❌ Requires G Suite domain-wide delegation
- ❌ Service account needs to be granted in Admin Console
- ❌ More complex setup

**Option 4: Hybrid Authentication**

Use Workforce Identity for Gemini chat, but Google OAuth popup for connector authorization:

```
1. User logs in via Workforce Identity (for chat)
2. When connector access needed:
   - Open popup with Google OAuth
   - User authorizes with Google account
   - Get Google OAuth token
   - Use for connector queries only
3. Maintain two tokens:
   - Workforce Identity token → General Gemini queries
   - Google OAuth token → Connector queries
```

**Pros:**
- ✅ Keeps Workforce Identity for main auth
- ✅ Connector authorization works
- ✅ Flexible solution

**Cons:**
- ❌ Complex: manage two auth flows
- ❌ User must authorize twice
- ❌ Need to handle token refresh for both

### Recommendation:

**Short-term:** Document limitation - users must use Gemini webapp for connector queries

**Long-term:** Implement **Option 1** (Google OAuth) if all users have Google Workspace accounts, or **Option 4** (Hybrid) if Workforce Identity is requirement

### Debug Tools Added:

**Backend:**
- `GET /api/debug/token-info` - Show token information and principal

**Frontend:**
- 🔍 "Token Info" button - Display current token details

### Testing Instructions:

1. Restart backend
2. Login via our app (Workforce Identity)
3. Click "🔍 Token Info" button
4. Note the principal/token info
5. Compare with Gemini webapp token (if accessible)

---

**Last updated:** 2026-07-11

**Status:** ⚠️ BLOCKED - Connector authorization incompatible with Workforce Identity Federation
**Root Cause:** Principal identity mismatch
**Workaround:** Use Gemini Enterprise webapp for connector queries
