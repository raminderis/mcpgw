package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cutmenot.ai/mcpgw/gwsvc/cmd/neo4jClient"
	"github.com/joho/godotenv"
)

const (
	defaultAuthToken     = "123mytoken"
	defaultAIRegistryURL = "http://airegistry:9098/registries/by-name"
)

type registryServiceInfo struct {
	Endpoint string
	Username string
	Password string
}

type registryResponse struct {
	Endpoint string `json:"endpoint"`
	Metadata struct {
		Auth struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auth"`
	} `json:"metadata"`
}

func main() {
	_ = godotenv.Load()
	mcpServerEndpoint := resolveEndpoint()
	client := &http.Client{}
	registryInfo, err := fetchRegistryInfo("postgres-mcp")
	if err != nil {
		log.Printf("failed to fetch registry info: %v", err)
	}

	target_service := registryInfo.Endpoint
	target_username := registryInfo.Username
	target_password := registryInfo.Password
	var sessionID string
	sessionID = postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"target_service":  target_service,
		"target_username": target_username,
		"target_password": target_password,
		"mcp_body": map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params":  map[string]any{},
		},
	})
	fmt.Println("session Id after INIT ", sessionID)
	postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"target_service":  target_service,
		"target_username": target_username,
		"target_password": target_password,
		"mcp_body": map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/initialized",
		},
	})

	postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"target_service":  target_service,
		"target_username": target_username,
		"target_password": target_password,
		"mcp_body": map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
			"params":  map[string]any{},
		},
	})
}

func postRPC(client *http.Client, endpoint, sessionID string, msg map[string]any) string {
	b, _ := json.Marshal(msg)
	fmt.Println("Sent:", string(b))
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+resolveAuthToken())
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error:", err)
		return sessionID
	}
	defer resp.Body.Close()
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		sessionID = id
		fmt.Println("Session ID: ", sessionID)
	}
	body, _ := io.ReadAll(resp.Body)

	var tools neo4jClient.ToolsListResult
	if err := json.Unmarshal(body, &tools); err == nil && len(tools.Tools) > 0 {
		fmt.Println("TOOLS:")
		for _, t := range tools.Tools {
			fmt.Println(" Name :", t.Name, " Title: ", t.Annotations.Title)
		}
	} else {
		fmt.Println("RESULT: ", string(body))
	}
	return sessionID
}

func resolveEndpoint() string {
	if endpoint := os.Getenv("MCPGW_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	port := os.Getenv("MCP_GW_SVC_PORT")
	if port == "" {
		port = "9090"
	}
	return fmt.Sprintf("http://gwsvc:%s/mcp", port)
}

func resolveAuthToken() string {
	if token := strings.TrimSpace(os.Getenv("MCPGW_AUTH_TOKEN")); token != "" {
		return token
	}
	return defaultAuthToken
}

func fetchRegistryInfo(serviceName string) (*registryServiceInfo, error) {
	registryURL := os.Getenv("AIREGISTRY_URL")
	if registryURL == "" {
		registryURL = defaultAIRegistryURL
	}

	// Build query URL
	queryURL := fmt.Sprintf("%s?name=%s", registryURL, serviceName)
	log.Printf("fetching registry info from: %s", queryURL)

	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, queryURL, nil)
	if err != nil {
		log.Printf("registry request creation failed: %v", err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("registry request failed: %v", err)
		return nil, fmt.Errorf("failed to fetch registry info: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("registry response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		log.Printf("registry returned non-200 status: %d", resp.StatusCode)
		return nil, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	var regResp registryResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		log.Printf("failed to decode registry response: %v", err)
		return nil, fmt.Errorf("failed to decode registry response: %w", err)
	}

	info := &registryServiceInfo{
		Endpoint: regResp.Endpoint,
		Username: regResp.Metadata.Auth.Username,
		Password: regResp.Metadata.Auth.Password,
	}

	log.Printf("registry info parsed: endpoint=%s username=%s", info.Endpoint, info.Username)

	return info, nil
}
