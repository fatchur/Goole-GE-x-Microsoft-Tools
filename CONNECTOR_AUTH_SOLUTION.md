# Connector Authorization Solution - Based on Investigation

## Discovery Summary

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
