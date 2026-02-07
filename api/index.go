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
	"strings" // Added for URL path checking
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
	Key    string `json:"key"`
	TOS    string `json:"tos"`
	Model  string `json:"model"`
	Type   string `json:"type"`
	Locale string `json:"locale"`
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

// --- Logic ---

func GenerateKeys() (string, string, error) {
	var privKey [32]byte
	if _, err := rand.Read(privKey[:]); err != nil {
		return "", "", err
	}
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
	req, _ := http.NewRequest("POST", "https://api.cloudflareclient.com/v0a2404/reg", bytes.NewBuffer(jsonData))
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

func UploadBatch(configs []GeneratedConfig, accountType string) map[string]interface{} {
	url := fmt.Sprintf("%s/admin/api/configs", BaseURL)
	payload := UploadPayload{
		Configs: configs,
		Type:    accountType,
	}

	jsonData, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-auth-key", os.Getenv("ADMIN_PASSWORD"))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"status": "error", "message": err.Error()}
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	return map[string]interface{}{
		"status":       "success",
		"account_type": accountType,
		"count":        len(configs),
		"response":     string(bodyBytes),
	}
}

// --- Vercel Handler ---

func Handler(w http.ResponseWriter, r *http.Request) {
	// Detect AccountType based on URL content
	path := strings.ToLower(r.URL.Path)
	accountType := "free" // Default

	if strings.Contains(path, "/pro") {
		accountType = "pro"
	} else if strings.Contains(path, "/free") {
		accountType = "free"
	}

	var validConfigs []GeneratedConfig
	var mu sync.Mutex
	var wg sync.WaitGroup

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

	var uploadResult map[string]interface{}
	if len(validConfigs) > 0 {
		uploadResult = UploadBatch(validConfigs, accountType)
	} else {
		uploadResult = map[string]interface{}{"status": "failed", "message": "No valid keys generated"}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(uploadResult)
}
