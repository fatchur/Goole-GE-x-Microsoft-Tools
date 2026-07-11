# Hybrid Authentication Implementation Plan

## Problem
- Microsoft SSO (Workforce Identity) is mandatory for main login
- Connector authorization requires Google OAuth principal
- Workforce Identity principal ≠ Google OAuth principal
- Users cannot access connector data despite authorization via Gemini webapp

## Solution: Hybrid Authentication
Maintain TWO authentication flows:
1. **Primary:** Microsoft Entra ID → Workforce Identity (for general chat)
2. **Secondary:** Google OAuth (for connector access only)

---

## Architecture

### Session Structure
```go
type UserSession struct {
    // Existing fields (Workforce Identity)
    Name                string
    Email               string
    GoogleAccessToken   string  // Workforce Identity token
    GoogleTokenExpiry   time.Time
    CreatedAt           time.Time

    // NEW: Google OAuth token for connectors
    ConnectorToken      string  // Google OAuth token
    ConnectorTokenExpiry time.Time
    ConnectorAuthorized  bool
}
```

### Authentication Flow

**1. Initial Login (Microsoft SSO)**
```
User → /auth/login → Microsoft Entra ID
    → /auth/callback → Workforce Identity token
    → Session created with Workforce token
    → User can chat (general queries) ✅
```

**2. Connector Authorization (Google OAuth)**
```
User queries connector data → Detection: needs authorization
    → Frontend shows: "Authorize Google Account"
    → Popup: /auth/google/login
    → Google OAuth flow → /auth/google/callback
    → Get Google OAuth token
    → Store in session as ConnectorToken
    → User can query connectors ✅
```

### Token Usage Strategy

**Backend decision logic:**
```go
func (h *Handler) Chat(c *fiber.Ctx) error {
    sess := getCurrentSession()

    // Detect if query needs connector access
    needsConnector := detectConnectorQuery(body.Message)

    var token string
    if needsConnector && sess.ConnectorAuthorized {
        // Use Google OAuth token for connector queries
        token = sess.ConnectorToken
    } else {
        // Use Workforce Identity token for general queries
        token = sess.GoogleAccessToken
    }

    raw, err := h.geminiClient.Ask(token, body.Message)
    // ...
}
```

---

## Implementation Steps

### Phase 1: Add Google OAuth Flow (Backend)

**1.1. Update .env**
```bash
# Add Google OAuth Web credentials
GOOGLE_OAUTH_CLIENT_ID=xxx.apps.googleusercontent.com
GOOGLE_OAUTH_CLIENT_SECRET=xxx
GOOGLE_OAUTH_REDIRECT_URI=http://localhost:8080/auth/google/callback
```

**1.2. Create Google OAuth Client**
```go
// internal/googleoauth/oauth.go
package googleoauth

import (
    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"
)

func NewConfig(clientID, clientSecret, redirectURI string) *oauth2.Config {
    return &oauth2.Config{
        ClientID:     clientID,
        ClientSecret: clientSecret,
        RedirectURL:  redirectURI,
        Scopes: []string{
            "openid",
            "profile",
            "email",
            "https://www.googleapis.com/auth/cloud-platform",
        },
        Endpoint: google.Endpoint,
    }
}
```

**1.3. Add Routes**
```go
// main.go
app.Get("/auth/google/login", h.GoogleLogin)
app.Get("/auth/google/callback", h.GoogleCallback)
```

**1.4. Update Session Store**
```go
// internal/session/store.go
type UserSession struct {
    // ... existing fields

    // Google OAuth for connectors
    ConnectorToken       string
    ConnectorTokenExpiry time.Time
    ConnectorAuthorized  bool
}
```

**1.5. Implement Handlers**
```go
// internal/handlers/google_oauth.go
func (h *Handler) GoogleLogin(c *fiber.Ctx) error {
    state, _ := randomString(16)
    // Save state in cookie
    // Redirect to Google OAuth
}

func (h *Handler) GoogleCallback(c *fiber.Ctx) error {
    // Validate state
    // Exchange code for token
    // Update existing session with ConnectorToken
    // Close popup window
}
```

### Phase 2: Query Detection & Token Selection

**2.1. Connector Query Detection**
```go
// internal/gemini/detector.go
func DetectConnectorQuery(query string) bool {
    connectorKeywords := []string{
        "email", "mail", "inbox",
        "calendar", "meeting", "appointment",
        "contact", "kontak",
        "outlook",
    }

    queryLower := strings.ToLower(query)
    for _, keyword := range connectorKeywords {
        if strings.Contains(queryLower, keyword) {
            return true
        }
    }
    return false
}
```

**2.2. Update Chat Handler**
```go
func (h *Handler) Chat(c *fiber.Ctx) error {
    sess, _, ok := h.currentSession(c)
    // ... existing checks

    needsConnector := gemini.DetectConnectorQuery(body.Message)

    var token string
    if needsConnector {
        if !sess.ConnectorAuthorized {
            return c.JSON(chatResponse{
                ConnectorAuthCheck: &struct{
                    NeedsAuthorization bool
                    NeedsGoogleAuth   bool
                }{
                    NeedsAuthorization: true,
                    NeedsGoogleAuth:    true,
                },
            })
        }

        if time.Now().After(sess.ConnectorTokenExpiry) {
            return c.Status(401).JSON(fiber.Map{
                "error": "Google OAuth token expired, please re-authorize",
            })
        }

        token = sess.ConnectorToken
        fmt.Println("[DEBUG] Using Google OAuth token for connector query")
    } else {
        token = sess.GoogleAccessToken
        fmt.Println("[DEBUG] Using Workforce Identity token for general query")
    }

    raw, err := h.geminiClient.Ask(token, body.Message)
    // ... rest of handler
}
```

### Phase 3: Frontend Implementation

**3.1. Update Authorization Warning**
```javascript
// FE/index.html

async function authorizeGoogleAccount() {
  const width = 500;
  const height = 600;
  const left = (screen.width - width) / 2;
  const top = (screen.height - height) / 2;

  const popup = window.open(
    `${BACKEND_URL}/auth/google/login`,
    'Google Authorization',
    `width=${width},height=${height},left=${left},top=${top}`
  );

  // Listen for popup close
  const checkPopup = setInterval(() => {
    if (popup.closed) {
      clearInterval(checkPopup);
      console.log('[DEBUG] Google auth popup closed, checking status...');
      checkConnectorAuth();
    }
  }, 500);
}

async function checkConnectorAuth() {
  const res = await fetch(`${BACKEND_URL}/api/me`, {
    credentials: 'include'
  });
  const data = await res.json();

  if (data.connectorAuthorized) {
    console.log('[DEBUG] ✅ Connector authorized!');
    needsConnectorAuth.value = false;
    alert('✅ Google account authorized! You can now query your emails and calendar.');
  } else {
    console.log('[DEBUG] ❌ Connector not yet authorized');
  }
}
```

**3.2. Update Warning Banner**
```html
<div v-if="needsConnectorAuth" class="auth-warning">
  <div class="auth-warning-icon">⚠️</div>
  <div class="auth-warning-content">
    <div class="auth-warning-title">Google Account Authorization Required</div>
    <div class="auth-warning-text">
      To access your Outlook emails, calendar, and contacts, you need to authorize your Google account.
      This is a one-time authorization.
    </div>
    <button class="auth-warning-btn" @click="authorizeGoogleAccount">
      🔐 Authorize Google Account
    </button>
  </div>
</div>
```

### Phase 4: Testing

**4.1. Test Flow**
1. Login with Microsoft → Workforce Identity ✅
2. Query general: "Jelaskan apa itu AI" → Works ✅
3. Query connector: "Ringkas email terbaru" → Shows Google auth warning
4. Click "Authorize Google Account" → Popup opens
5. Select Google account → Authorize → Popup closes
6. Query again: "Ringkas email terbaru" → Works! ✅

**4.2. Token Refresh**
- Workforce Identity token: Auto refresh via STS (existing)
- Google OAuth token: Implement refresh token flow

---

## Security Considerations

1. **Two tokens in session**
   - Store both securely in backend session
   - Never expose to frontend
   - Clear both on logout

2. **Token selection**
   - Automatic based on query content
   - Log which token used for audit

3. **Scope limitation**
   - Workforce Identity: cloud-platform
   - Google OAuth: cloud-platform (same scopes for consistency)

4. **CSRF protection**
   - State parameter for both flows
   - Separate state cookies

---

## Alternative: Simpler Approach

If full hybrid auth is too complex, consider **Manual Toggle**:

```javascript
// Add toggle in UI
<label>
  <input type="checkbox" v-model="useGoogleAuth">
  Use Google account for this query
</label>

// Backend accepts parameter
POST /api/chat
{
  "message": "...",
  "useConnectorToken": true  // Frontend sends this
}
```

User manually selects when to use Google token vs Workforce token.

**Pros:** Simpler implementation
**Cons:** Manual user action required

---

## Recommendation

Start with **Manual Toggle** approach for quick implementation, then enhance to **Automatic Detection** based on user feedback.

**Estimated Effort:**
- Manual Toggle: 2-3 hours
- Full Automatic: 6-8 hours

**Next Step:**
Decide which approach to implement based on:
- Time available
- User experience requirements
- Complexity tolerance
