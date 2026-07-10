package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type WebsiteClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewWebsiteClient(baseURL string) *WebsiteClient {
	return &WebsiteClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type BindLicenseRequest struct {
	LicenseKey        string `json:"license_key"`
	MachineFingerprint string `json:"machine_fingerprint"`
}

type BindLicenseResponse struct {
	LicenseKey string    `json:"license_key"`
	Version    string    `json:"version"`
	ExpiresAt  time.Time `json:"expires_at"`
	Modules    []string  `json:"modules"`
}

type VerifyLicenseResponse struct {
	Valid     bool      `json:"valid"`
	Version   string    `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
	Modules   []string  `json:"modules"`
}

func (c *WebsiteClient) BindLicense(licenseKey, machineFP string) (*BindLicenseResponse, error) {
	reqBody := BindLicenseRequest{
		LicenseKey:         licenseKey,
		MachineFingerprint: machineFP,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/licenses/bind", c.baseURL)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bind license failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result BindLicenseResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

func (c *WebsiteClient) UnbindLicense(licenseKey, machineFP string) error {
	reqBody := BindLicenseRequest{
		LicenseKey:         licenseKey,
		MachineFingerprint: machineFP,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/licenses/unbind", c.baseURL)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unbind license failed: status %d", resp.StatusCode)
	}

	return nil
}

func (c *WebsiteClient) VerifyLicense(licenseKey, machineFP string) (*VerifyLicenseResponse, error) {
	reqBody := BindLicenseRequest{
		LicenseKey:         licenseKey,
		MachineFingerprint: machineFP,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/licenses/verify", c.baseURL)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("verify license failed: status %d", resp.StatusCode)
	}

	var result VerifyLicenseResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

func (c *WebsiteClient) DownloadModule(moduleName, token string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/modules/%s/download?token=%s", c.baseURL, moduleName, token)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download module failed: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return data, nil
}