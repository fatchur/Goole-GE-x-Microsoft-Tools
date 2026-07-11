// Package gemini - Connector authorization detection utilities
package gemini

import (
	"encoding/json"
	"strings"
)

// ConnectorAuthStatus represents the authorization status of a connector
type ConnectorAuthStatus struct {
	NeedsAuthorization bool     `json:"needsAuthorization"`
	DetectedKeywords   []string `json:"detectedKeywords,omitempty"`
	ResponseText       string   `json:"responseText,omitempty"`
}

// DetectConnectorAuthStatus analyzes Gemini Enterprise response to determine
// if the user needs to authorize connectors
func DetectConnectorAuthStatus(responseJSON json.RawMessage) (*ConnectorAuthStatus, error) {
	var response []map[string]interface{}
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return nil, err
	}

	status := &ConnectorAuthStatus{
		NeedsAuthorization: false,
		DetectedKeywords:   []string{},
	}

	// Keywords that indicate connector needs authorization (case-insensitive)
	authKeywords := []string{
		"belum dikonfigurasi",
		"tidak memiliki akses",
		"hubungi administrator",
		"configure connector",
		"setup required",
		"connection not configured",
		"koneksi data",
		"belum aktif",
		"needs authorization",
		"authorize access",
		"grant permission",
	}

	// Collect all text from streaming response
	var fullText strings.Builder

	for _, chunk := range response {
		if answer, ok := chunk["answer"].(map[string]interface{}); ok {
			if replies, ok := answer["replies"].([]interface{}); ok {
				for _, reply := range replies {
					if replyMap, ok := reply.(map[string]interface{}); ok {
						if groundedContent, ok := replyMap["groundedContent"].(map[string]interface{}); ok {
							if content, ok := groundedContent["content"].(map[string]interface{}); ok {
								if text, ok := content["text"].(string); ok {
									fullText.WriteString(text)
								}
							}
						}
					}
				}
			}
		}
	}

	status.ResponseText = fullText.String()
	lowerText := strings.ToLower(status.ResponseText)

	// Check for authorization keywords
	for _, keyword := range authKeywords {
		if strings.Contains(lowerText, strings.ToLower(keyword)) {
			status.NeedsAuthorization = true
			status.DetectedKeywords = append(status.DetectedKeywords, keyword)
		}
	}

	return status, nil
}
