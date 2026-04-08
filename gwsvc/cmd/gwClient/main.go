package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"cutmenot.ai/mcpgw/gwsvc/cmd/neo4jClient"
	"github.com/joho/godotenv"
)

const defaultAuthToken = "123mytoken"

func main() {
	_ = godotenv.Load()
	mcpServerEndpoint := resolveEndpoint()
	client := &http.Client{}
	var sessionID string
	sessionID = postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})

	postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	postRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
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
