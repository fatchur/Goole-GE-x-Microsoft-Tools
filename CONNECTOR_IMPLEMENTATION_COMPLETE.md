# Connector Authorization - Complete Implementation

## Problem Solved

**Discovery:** Gemini Enterprise uses TWO separate Microsoft App Registrations:
1. **SSO Login App** - For user login (Workforce Identity)
2. **Connector App** - For Outlook connector authorization

**Root Cause:** We only used SSO App credentials, connector needs separate authorization with Connector App credentials.

**Solution:** Implement dual OAuth flow - keep SSO for login, add Connector OAuth for Outlook access.

---

## Architecture

### Two Authentication Flows:

```
┌─────────────────────────────────────────────────────────────┐
│                    PRIMARY FLOW: SSO LOGIN                  │
├─────────────────────────────────────────────────────────────┤
│ User → Microsoft Entra ID (SSO App)                         │
│      → Workforce Identity Federation                        │
│      → Google STS Token                                     │
│      → Use for: General Gemini chat queries ✅              │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│               SECONDARY FLOW: CONNECTOR AUTH                │
├─────────────────────────────────────────────────────────────┤
│ User → Microsoft Entra ID (Connector App)                   │
│      → Microsoft Graph API Token                            │
│      → Use for: Outlook connector queries ✅                │
└─────────────────────────────────────────────────────────────┘
```

### Credentials:

**SSO App Registration:**
```env
ENTRA_CLIENT_ID=ac708c4b-8590-406d-a113-bf75403754e9
ENTRA_CLIENT_SECRET=[REDACTED - see .env file]
ENTRA_REDIRECT_URI=http://localhost:8080/auth/callback
Scope: openid profile email User.Read
```

**Connector App Registration:**
```env
CONNECTOR_CLIENT_ID=f2e7e1f8-9815-4e5d-ab94-4e1a16727041
CONNECTOR_CLIENT_SECRET=[REDACTED - see .env file]
CONNECTOR_REDIRECT_URI=http://localhost:8080/auth/connector/callback
Scope: https://graph.microsoft.com/.default offline_access
```

---

## Implementation

### Phase 1: Backend Configuration ✅

**Files Modified:**
- `internal/config/config.go` - Added connector credentials
- `internal/session/store.go` - Added connector token fields
- `.env` - Added connector environment variables

**Created:**
- `internal/connector/connector.go` - Connector OAuth client

### Phase 2: Backend Handlers (Next)

**Routes to add:**
```go
app.Get("/auth/connector/login", h.ConnectorLogin)
app.Get("/auth/connector/callback", h.ConnectorCallback)
app.Get("/api/connector/status", h.ConnectorStatus)
```

**Handler Logic:**

```go
// ConnectorLogin - Initiate connector OAuth flow
func (h *Handler) ConnectorLogin(c *fiber.Ctx) error {
    state := randomString(16)
    // Save state
    // Redirect to Microsoft OAuth (Connector App)
}

// ConnectorCallback - Handle OAuth callback
func (h *Handler) ConnectorCallback(c *fiber.Ctx) error {
    // Validate state
    // Exchange code for token
    // Update existing session with connector token
    // Close popup window
}

// ConnectorStatus - Check if connector authorized
func (h *Handler) ConnectorStatus(c *fiber.Ctx) error {
    sess := getCurrentSession()
    return c.JSON(fiber.Map{
        "authorized": sess.ConnectorAuthorized,
        "expiry": sess.ConnectorTokenExpiry,
    })
}
```

**Chat Handler Update:**

```go
func (h *Handler) Chat(c *fiber.Ctx) error {
    sess := getCurrentSession()

    // Detect if query needs connector
    needsConnector := gemini.DetectConnectorQuery(body.Message)

    var token string
    if needsConnector {
        if !sess.ConnectorAuthorized {
            return c.JSON(chatResponse{
                ConnectorAuthCheck: &struct{
                    NeedsAuthorization bool
                    NeedsConnectorAuth bool
                }{
                    NeedsAuthorization: true,
                    NeedsConnectorAuth: true,
                },
            })
        }

        // Check if connector token expired
        if time.Now().After(sess.ConnectorTokenExpiry) {
            // Try refresh
            err := h.refreshConnectorToken(sess)
            if err != nil {
                return c.Status(401).JSON(fiber.Map{
                    "error": "Connector token expired, please re-authorize",
                })
            }
        }

        token = sess.ConnectorAccessToken
    } else {
        token = sess.GoogleAccessToken
    }

    // Call Gemini with selected token
    raw, err := h.geminiClient.Ask(token, body.Message)
    // ...
}
```

### Phase 3: Frontend Integration (Next)

**Update warning banner:**

```html
<div v-if="needsConnectorAuth" class="auth-warning">
  <div class="auth-warning-icon">⚠️</div>
  <div class="auth-warning-content">
    <div class="auth-warning-title">Outlook Connector Authorization Required</div>
    <div class="auth-warning-text">
      To access your Outlook emails, calendar, and contacts, you need to authorize
      the connector. This is a one-time authorization using your Microsoft account.
    </div>
    <button class="auth-warning-btn" @click="authorizeConnector">
      🔐 Authorize Outlook Connector
    </button>
  </div>
</div>
```

**Authorization function:**

```javascript
async function authorizeConnector() {
  const width = 500;
  const height = 600;
  const left = (screen.width - width) / 2;
  const top = (screen.height - height) / 2;

  const popup = window.open(
    `${BACKEND_URL}/auth/connector/login`,
    'Connector Authorization',
    `width=${width},height=${height},left=${left},top=${top}`
  );

  // Poll for popup close
  const checkPopup = setInterval(() => {
    if (popup.closed) {
      clearInterval(checkPopup);
      checkConnectorStatus();
    }
  }, 500);
}

async function checkConnectorStatus() {
  const res = await fetch(`${BACKEND_URL}/api/connector/status`, {
    credentials: 'include'
  });
  const data = await res.json();

  if (data.authorized) {
    needsConnectorAuth.value = false;
    alert('✅ Outlook connector authorized! You can now query your emails and calendar.');
  }
}
```

---

## User Experience Flow

### Initial State:
1. User logs in with Microsoft (SSO) ✅
2. Can use general chat features ✅
3. Connector NOT authorized yet ⚠️

### When User Queries Connector Data:

**Scenario 1: First Time**
```
User: "Ringkas email terbaru saya"
  ↓
Backend detects connector query
  ↓
Check: Connector authorized? NO
  ↓
Response: { needsConnectorAuth: true }
  ↓
Frontend shows warning banner
  ↓
User clicks "Authorize Outlook Connector"
  ↓
Popup opens → Microsoft OAuth (Connector App)
  ↓
User selects account & authorizes
  ↓
Callback → Backend saves connector token
  ↓
Popup closes → Frontend checks status
  ↓
Status: authorized = true
  ↓
User queries again: "Ringkas email terbaru saya"
  ↓
Backend uses connector token
  ↓
SUCCESS! 🎉
```

**Scenario 2: Already Authorized**
```
User: "Tampilkan kalender minggu ini"
  ↓
Backend detects connector query
  ↓
Check: Connector authorized? YES
  ↓
Check: Token expired? NO
  ↓
Use connector token for query
  ↓
SUCCESS! ✅
```

**Scenario 3: Token Expired**
```
User: "Cari email dari John"
  ↓
Backend detects connector query
  ↓
Check: Connector authorized? YES
  ↓
Check: Token expired? YES
  ↓
Backend refreshes token automatically
  ↓
Use new connector token
  ↓
SUCCESS! ✅
```

---

## Token Management

### Session Structure:
```go
type UserSession struct {
    // SSO/Login tokens
    GoogleAccessToken string
    GoogleTokenExpiry time.Time

    // Connector tokens
    ConnectorAccessToken  string
    ConnectorRefreshToken string
    ConnectorTokenExpiry  time.Time
    ConnectorAuthorized   bool

    // User info
    Name, Email, CreatedAt
}
```

### Token Selection Logic:
```
Query received
  ↓
Detect if connector needed (email/calendar/contact keywords)
  ↓
If connector:
  - Use ConnectorAccessToken
  - Auto-refresh if expired
Else:
  - Use GoogleAccessToken (Workforce Identity)
```

---

## Security Considerations

1. **Two Separate Tokens**
   - Never expose to frontend
   - Stored in backend session only
   - httpOnly cookies

2. **Token Refresh**
   - Connector token: Microsoft refresh token flow
   - Workforce token: STS token exchange

3. **Clear on Logout**
   - Clear both tokens
   - Delete session
   - Clear all cookies

4. **Scope Limitation**
   - SSO: openid, profile, email
   - Connector: Microsoft Graph .default (Outlook data only)

---

## Testing Checklist

- [ ] Backend restart with new .env
- [ ] Login with Microsoft (SSO flow)
- [ ] Query general: "Jelaskan AI" → Uses Workforce token
- [ ] Query connector: "Ringkas email" → Shows auth warning
- [ ] Click authorize → Popup opens
- [ ] Select account → Authorize → Popup closes
- [ ] Query again: "Ringkas email" → Uses connector token → SUCCESS
- [ ] Logout → Both tokens cleared
- [ ] Login again → Connector still needs re-auth (tokens cleared)

---

## Advantages of This Approach

✅ **Uses EXACT credentials as Gemini webapp**
✅ **Same OAuth flow as Gemini**
✅ **Token compatibility guaranteed**
✅ **Maintains Workforce Identity for SSO**
✅ **One-time connector authorization**
✅ **Automatic token refresh**
✅ **Transparent token selection**

---

## Next Steps

1. Implement connector handlers (30 min)
2. Update chat handler with token selection (15 min)
3. Implement frontend authorization flow (30 min)
4. Test end-to-end (30 min)

**Total estimated time:** 2 hours

**Ready to proceed with implementation?**
