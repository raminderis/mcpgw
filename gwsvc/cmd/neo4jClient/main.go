package neo4jClient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	mcpServerEndpoint := "http://localhost:9094/mcp"
	client := &http.Client{}
	var sessionID string
	sessionID, _ = PostRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})

	PostRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type ToolsListResult struct {
	Tools []struct {
		Name        string `json:"name"`
		Annotations struct {
			Title string `json:"title"`
		} `json:"annotations"`
	} `json:"tools"`
}

func PostRPC(client *http.Client, endpoint, sessionID string, msg map[string]any) (string, ToolsListResult) {
	b, _ := json.Marshal(msg)
	fmt.Println("Sent:", string(b))
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	neo4jUser := os.Getenv("NEO4J_USERNAME")
	if neo4jUser == "" {
		neo4jUser = "neo4j"
	}
	neo4jPass := os.Getenv("NEO4J_PASSWORD")
	req.SetBasicAuth(neo4jUser, neo4jPass)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error:", err)
		return sessionID, ToolsListResult{}
	}
	defer resp.Body.Close()
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		sessionID = id
		fmt.Println("Session ID: ", sessionID)
	}
	body, _ := io.ReadAll(resp.Body)
	var tools ToolsListResult
	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err == nil {
		if rpcResp.Error != nil {
			fmt.Println("RPC Error: ", rpcResp.Error.Message)
		} else if rpcResp.Result != nil {
			if err := json.Unmarshal(rpcResp.Result, &tools); err == nil && len(tools.Tools) > 0 {
				fmt.Println("TOOLS:")
				for _, t := range tools.Tools {
					fmt.Println(" Name :", t.Name, " Title: ", t.Annotations.Title)
				}
			} else {
				fmt.Println("RESULT: ", string(rpcResp.Result))
			}
		}
	}
	// fmt.Println("RESPONSE:", string(body))
	return sessionID, tools
}
