package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

// --- Settings ---
// Set ADMIN_PASSWORD in your Vercel Environment Variables!
var (
	BaseURL       = "https://h2api.arakan.info"
	GenerateCount = 5
	AccountType   = "free"
)

// --- Structs for JSON Parsing ---

type CloudflareRequest struct {
	Key       string `json:"key"`
	InstallID string `json:"install_id"`
	FCMToken  string `json:"fcm_token"`
	TOS       string `json:"tos"`
	Model     string `json:"model"`
	Type      string `json:"type"`
	Locale    string `json:"locale"`
}

type CloudflareResponse struct {
	Config struct {
		Interface struct {
			Addresses struct {
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

type GeneratedConfig struct {
	Password string `json:"password"`
	IP       string `json:"ip"`
	Server   string `json:"server"`
}

type UploadPayload struct {
	Configs []GeneratedConfig `json:"configs"`
	Type    string            `json:"type"`
}

// --- Core Logic ---

// GenerateKeys creates a Curve25519 key pair and returns them as Base64 strings
func GenerateKeys() (string, string, error) {
	var privKey [32]byte
	_, err := rand.Read(privKey[:])
	if err != nil {
		return "", "", err
	}

	// Clamp the key (standard X25519 procedure)
	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	var pubKey [32]byte
	curve25519.ScalarBaseMult(&pubKey, &privKey)

	privB64 := base64.StdEncoding.EncodeToString(privKey[:])
	pubB64 := base64.StdEncoding.EncodeToString(pubKey[:])

	return privB64, pubB64, nil
}

// RegisterWithCloudflare generates keys and registers them
func RegisterWithCloudflare() (*GeneratedConfig, error) {
	privB64, pubB64, err := GenerateKeys()
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000") + "+00:00"

	payload := CloudflareRequest{
		Key:    pubB64,
		TOS:    timestamp,
		Model:  "Android",
		Type:   "Android",
		Locale: "en_US",
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://api.cloudflareclient.com/v0a2404/reg", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cloudflare API error: %d", resp.StatusCode)
	}

	var cfResp CloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, err
	}

	return &GeneratedConfig{
		Password: privB64,
		IP:       cfResp.Config.Interface.Addresses.V6,
		Server:   "162.159.192.1",
	}, nil
}

// UploadBatch sends the configs to your server
func UploadBatch(configs []GeneratedConfig) map[string]interface{} {
	if len(configs) == 0 {
		return map[string]interface{}{"status": "error", "message": "No configs to upload"}
	}

	url := fmt.Sprintf("%s/admin/api/configs", BaseURL)
	payload := UploadPayload{
		Configs: configs,
		Type:    AccountType,
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}
	}

	req.Header.Set("Content-Type", "application/json")
	// Best Practice: Get password from Environment Variable
	req.Header.Set("x-auth-key", os.Getenv("ADMIN_PASSWORD")) 
	req.Header.Set("User-Agent", "Go-VercelBot")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	
	return map[string]interface{}{
		"status":      "success",
		"server_code": resp.StatusCode,
		"count":       len(configs),
		"response":    string(bodyBytes),
	}
}

// --- Vercel Handler ---

// Handler is the entry point for Vercel
func Handler(w http.ResponseWriter, r *http.Request) {
	var validConfigs []GeneratedConfig
	var mu sync.Mutex // Mutex to safely append to slice
	var wg sync.WaitGroup

	// Run concurrently
	for i := 0; i < GenerateCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			config, err := RegisterWithCloudflare()
			if err == nil && config != nil {
				mu.Lock()
				validConfigs = append(validConfigs, *config)
				mu.Unlock()
			}
		}()
	}

	wg.Wait() // Wait for all goroutines to finish

	// Upload Logic
	var uploadResult map[string]interface{}
	if len(validConfigs) > 0 {
		uploadResult = UploadBatch(validConfigs)
	} else {
		uploadResult = map[string]interface{}{"status": "failed", "message": "No valid keys generated"}
	}

	// Response
	responseData := map[string]interface{}{
		"generated_count": len(validConfigs),
		"upload_result":   uploadResult,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}