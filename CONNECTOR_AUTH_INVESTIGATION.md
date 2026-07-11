# Gemini Enterprise Connector Authorization Investigation

## Discovery
User reported that Gemini Enterprise webapp has TWO separate authorization steps:
1. Login via Microsoft (Workforce Identity)
2. Authorize connector (separate step)
3. Then can access Outlook data

**This means:** Connector authorization CAN work with Workforce Identity principal!

## Investigation Plan

### Step 1: Capture Connector Authorization Flow

**Open Gemini Enterprise webapp in browser:**
1. Open Chrome DevTools (F12)
2. Go to Network tab
3. Click "Authorize" button for Outlook connector
4. Capture all network requests

**Key information to capture:**

#### 1.1. Authorization Request
- **URL** called when click "Authorize"
- **Method** (GET/POST)
- **Headers** (especially Authorization)
- **Query parameters**
- **Request body** (if POST)

#### 1.2. Redirect Flow
- **Redirect URL** to Microsoft
- **OAuth parameters** (client_id, redirect_uri, scope, etc.)
- **State parameter**

#### 1.3. Callback URL
- **Where Microsoft redirects back**
- **Is it Google-managed or can we intercept?**
- **Parameters in callback**

#### 1.4. Token Exchange/Storage
- **Does webapp call any API after callback?**
- **What endpoint stores connector authorization?**
- **Any special headers or tokens?**

### Step 2: Key Questions to Answer

**Q1: Initial Authorization Trigger**
```
When user clicks "Authorize" button, what happens?

Possible patterns:
A. Direct redirect to Microsoft OAuth
   → URL: https://login.microsoftonline.com/...

B. API call first, then redirect
   → POST /api/connector/authorize
   → Response contains authorization URL
   → Frontend redirects to URL

C. Popup window
   → window.open(authURL)
   → Different redirect_uri for popup
```

**Q2: Redirect URI**
```
Where does Microsoft redirect after authorization?

Known from documentation:
https://vertexaisearch.cloud.google.com/oauth-redirect

Questions:
- Can we specify custom redirect_uri?
- Does it accept wildcards or multiple URIs?
- Is there a way to register our own callback?
```

**Q3: Authorization Storage**
```
After successful authorization, how is it stored?

Possible mechanisms:
A. API call to store authorization
   → POST /v1alpha/projects/.../connectors/{id}/authorize
   → Body: { authorizationCode: "...", ... }

B. Automatic via Google's backend
   → No API call visible
   → Google maps user to connector internally

C. Session-based
   → Cookie or session updated
```

**Q4: Authorization Check**
```
How does webapp check if user has authorized connector?

Possible endpoints:
A. GET /v1alpha/projects/.../connectors/{id}/status
   → Response: { authorized: true/false }

B. Included in connector metadata
   → GET /v1alpha/projects/.../dataStores/{id}
   → Response includes user authorization status

C. Error-based detection (like our current approach)
   → No explicit check
   → Detect from query response
```

### Step 3: Network Capture Instructions

**Detailed capture procedure:**

1. **Clear browser cache and cookies**
   ```
   Chrome → Settings → Privacy → Clear browsing data
   Or use Incognito mode
   ```

2. **Open Gemini Enterprise webapp**
   ```
   URL: https://gemini.google.com/app
   Or: https://YOUR_GEMINI_ENTERPRISE_URL
   ```

3. **Login with Microsoft account**
   ```
   Watch Network tab during login
   Filter: "Fetch/XHR" or "All"
   ```

4. **Navigate to connector settings/page**
   ```
   Look for Outlook connector
   Should show "Authorize" button
   ```

5. **Open Network tab → Preserve log**
   ```
   ✅ Check "Preserve log" to keep requests during redirects
   ```

6. **Click "Authorize" button**
   ```
   Capture ALL requests:
   - Initial button click
   - Any API calls
   - Redirect to Microsoft
   - Callback from Microsoft
   - Any post-authorization calls
   ```

7. **Export HAR file**
   ```
   Right-click in Network tab → "Save all as HAR with content"
   Or manually note down:
   - URLs
   - Headers
   - Request/Response bodies
   ```

### Step 4: Analysis Checklist

**For each request captured, analyze:**

- [ ] Request URL (full path with query params)
- [ ] Request Method (GET/POST/PUT)
- [ ] Request Headers (especially):
  - [ ] Authorization header
  - [ ] Content-Type
  - [ ] Origin / Referer
  - [ ] Custom headers (x-goog-*, etc.)
- [ ] Request Body (if any)
- [ ] Response Status Code
- [ ] Response Headers
- [ ] Response Body
- [ ] Timing (order of requests)

### Step 5: Look for Patterns

**Pattern 1: Authorization URL Construction**
```javascript
// Look for JavaScript that constructs auth URL
// Search in Sources tab for:
- "authorize"
- "oauth"
- "login.microsoftonline.com"
- "vertexaisearch.cloud.google.com"
```

**Pattern 2: API Endpoints**
```
Look for calls to:
- /connectors/
- /dataStores/
- /authorize
- /oauth
- /callback
```

**Pattern 3: State Management**
```
Check for:
- Local storage changes
- Session storage changes
- Cookie changes
- Global variables (window.*)
```

### Step 6: Compare with Our Flow

**Our current flow:**
```
1. User login → Microsoft Entra ID
2. Backend gets ID Token
3. Backend exchange → Google STS token (Workforce Identity)
4. Backend stores token in session
5. User queries → Uses Workforce token
6. Connector access → FAILS (wrong principal)
```

**Gemini webapp flow (to be discovered):**
```
1. User login → Microsoft (Workforce Identity?)
2. User clicks Authorize → ???
3. ??? → Microsoft OAuth for connector
4. Callback → ???
5. ??? → Connector authorized
6. User queries → SUCCESS
```

**Key differences to identify:**
- How does Gemini webapp handle connector authorization POST-login?
- What API endpoints do they use?
- Can we replicate this flow in our app?

### Step 7: Potential Discoveries

**Best case scenario:**
```
Find API endpoint like:
POST /v1alpha/projects/{project}/locations/{location}/connectors/{connectorId}:authorize

That we can call with our Workforce Identity token
to trigger connector authorization flow.
```

**Expected scenario:**
```
Discover redirect_uri that we can potentially use
or mechanism to register our own callback URL.
```

**Worst case scenario:**
```
Flow is completely internal to Google's infrastructure
and not replicable in custom apps.

→ Need to use hybrid auth approach instead.
```

## Next Steps

1. **Immediate:** Capture network traffic as described above
2. **Analyze:** Look for authorization API endpoints
3. **Test:** Try replicating discovered flow in our app
4. **Document:** Update findings in this file
5. **Implement:** Build based on discovered mechanism

## Questions to Answer

- [ ] What is the exact URL flow when clicking "Authorize"?
- [ ] Is there an API to trigger authorization programmatically?
- [ ] Can we register custom redirect_uri for our app?
- [ ] How does Gemini map authorization to Workforce Identity principal?
- [ ] Is there a connector authorization status API?

## Capture Template

```
=== AUTHORIZATION FLOW CAPTURE ===

Date: _______________
Browser: Chrome/Firefox/Safari
Gemini Enterprise URL: _______________

=== STEP 1: Click Authorize Button ===

Initial Request:
URL:
Method:
Headers:
  Authorization:
  Content-Type:
Body:


Response:
Status:
Headers:
Body:


=== STEP 2: Redirect to Microsoft ===

Redirect URL:
Full URL with params:

OAuth Parameters:
  client_id:
  redirect_uri:
  scope:
  state:
  response_type:


=== STEP 3: Microsoft Callback ===

Callback URL:
Parameters:
  code:
  state:


=== STEP 4: Post-Authorization ===

Any API calls after callback:
1. URL:
   Method:
   Body:

2. URL:
   Method:
   Body:


=== STEP 5: Verification ===

Query test after authorization:
URL:
Response indicates authorized: YES/NO


=== END CAPTURE ===
```

---

## Expected Outcome

After this investigation, we should know:
1. ✅ Exact mechanism Gemini uses for connector authorization
2. ✅ Whether we can replicate it in our app
3. ✅ API endpoints (if any) for authorization management
4. ✅ Whether hybrid auth is necessary or can be avoided

This will determine our implementation path forward.
