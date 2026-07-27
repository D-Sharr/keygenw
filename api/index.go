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
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

// --- Settings ---
var (
	BaseURL       = "https://h2api.arakan.info"
	GenerateCount = 5
)

// --- Structs ---

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

func GenerateKeys() (string, string, error) {
	var privKey [32]byte
	if _, err := rand.Read(privKey[:]); err != nil {
		return "", "", err
	}
	// Clamp the key
	privKey[0] &= 248
	privKey[31] &= 127
	privKey[31] |= 64

	var pubKey [32]byte
	curve25519.ScalarBaseMult(&pubKey, &privKey)

	return base64.StdEncoding.EncodeToString(privKey[:]), base64.StdEncoding.EncodeToString(pubKey[:]), nil
}

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
		return nil, fmt.Errorf("CF Error: %d", resp.StatusCode)
	}

	var cfResp CloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, err
	}

	return &GeneratedConfig{
		Password: privB64,
		IP:       cfResp.Config.Interface.Addresses.V6,
		Server:   "162.159.192.10",
	}, nil
}

func UploadBatch(configs []GeneratedConfig, accType string) map[string]interface{} {
	url := fmt.Sprintf("%s/admin/api/configs", BaseURL)
	payload := UploadPayload{
		Configs: configs,
		Type:    accType,
	}

	jsonData, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}
	}

	req.Header.Set("Content-Type", "application/json")
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
		"type_sent":   accType,
		"response":    string(bodyBytes),
	}
}

// --- Vercel Handler ---

func Handler(w http.ResponseWriter, r *http.Request) {
	// 1. Identify Account Type from Query Parameter (?type=pro)
	rawType := r.URL.Query().Get("type")
	accountType := "free" // Default

	if strings.ToLower(rawType) == "pro" {
		accountType = "pro"
	}

	var validConfigs []GeneratedConfig
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 2. Concurrency Logic
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

	wg.Wait()

	// 3. Upload and Respond
	var uploadResult map[string]interface{}
	if len(validConfigs) > 0 {
		uploadResult = UploadBatch(validConfigs, accountType)
	} else {
		uploadResult = map[string]interface{}{"status": "failed", "message": "No valid keys generated"}
	}

	// Output result
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"requested_type": rawType,
		"applied_type":   accountType,
		"generated":      len(validConfigs),
		"upload_details": uploadResult,
	})
}
