// Package gemini - Data Stores API endpoints for connector management
package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ListDataStores returns all data stores in the collection
func (c *Client) ListDataStores(googleAccessToken string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/dataStores",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation,
	)

	fmt.Printf("[DEBUG] List Data Stores API Call:\n")
	fmt.Printf("  - URL: %s\n", url)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("list data stores API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// GetDataStore returns detailed information about a specific data store,
// including connector state and configuration
func (c *Client) GetDataStore(googleAccessToken, dataStoreID string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1/projects/%s/locations/%s/collections/default_collection/dataStores/%s",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, dataStoreID,
	)

	fmt.Printf("[DEBUG] Get Data Store API Call:\n")
	fmt.Printf("  - URL: %s\n", url)
	fmt.Printf("  - Data Store ID: %s\n", dataStoreID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("get data store API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// GetEngine returns engine configuration including connected data stores
func (c *Client) GetEngine(googleAccessToken string) (json.RawMessage, error) {
	url := fmt.Sprintf(
		"%s/v1alpha/projects/%s/locations/%s/collections/default_collection/engines/%s",
		c.baseURL(), c.cfg.GCPProjectNumber, c.cfg.GeminiLocation, c.cfg.GeminiAppID,
	)

	fmt.Printf("[DEBUG] Get Engine API Call:\n")
	fmt.Printf("  - URL: %s\n", url)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+googleAccessToken)
	req.Header.Set("x-goog-user-project", c.cfg.GCPProjectNumber)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("get engine API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}
