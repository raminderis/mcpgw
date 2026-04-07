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
	"sync"
	"time"

	"cutmenot.ai/mcpgw/gwsvc/cmd/neo4jClient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultGwsvcListenAddr  = ":9090"
	defaultNeo4jMCPEndpoint = "http://localhost:9094/mcp"
	defaultConfigFile       = "./gwsvc-config.json"
)

type gatewayConfig struct {
	Neo4jHTTPPort   int    `json:"neo4jHttpPort"`
	Neo4jMCPURL     string `json:"neo4jMcpUrl"`
	GwsvcListenPort int    `json:"gwsvcListenPort"`
	GwsvcListenAddr string `json:"gwsvcListenAddr"`
}

var (
	configMu   sync.RWMutex
	config     gatewayConfig
	httpServer *http.Server
)

func main() {
	_ = reloadConfigFromDisk()

	server := mcp.NewServer(&mcp.Implementation{Name: "mcpgw", Version: "v0.0.1"}, &mcp.ServerOptions{
		InitializedHandler: func(context.Context, *mcp.InitializedRequest) {
			fmt.Println("initialized!")
		},
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, nil)

	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("in handlefunc")
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var rpc struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &rpc)
		if rpc.Method == "tools/list" {
			fmt.Println("Intercepted tools/list -> forwarding it to neo4j mcp")
			neo4jResp := forwardToolsListToNeo4jRaw(body)
			respBytes, err := json.Marshal(neo4jResp)
			if err != nil {
				http.Error(w, "failed to marshal tools response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(respBytes)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})

	http.HandleFunc("/reload", handleReload)
	http.HandleFunc("/shutdown", handleShutdown)

	listenAddr := resolveGwsvcListenAddress()
	fmt.Printf("Running the mcp server on %s\n", listenAddr)

	httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: http.DefaultServeMux,
	}

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("error is %v", err)
	}
}

func forwardToolsListToNeo4jRaw(body []byte) neo4jClient.ToolsListResult {
	mcpServerEndpoint := getConfigNeo4jEndpoint()
	client := &http.Client{}
	var sessionID string
	sessionID, _ = neo4jClient.PostRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})

	_, tools := neo4jClient.PostRPC(client, mcpServerEndpoint, sessionID, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	return tools
}

func getConfigNeo4jEndpoint() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if config.Neo4jMCPURL != "" {
		return config.Neo4jMCPURL
	}
	if endpoint := os.Getenv("NEO4J_MCP_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return defaultNeo4jMCPEndpoint
}

func resolveNeo4jEndpoint() string {
	configFile := os.Getenv("GWSVC_CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	b, err := os.ReadFile(configFile)
	if err == nil {
		var cfg gatewayConfig
		if json.Unmarshal(b, &cfg) == nil && cfg.Neo4jMCPURL != "" {
			return cfg.Neo4jMCPURL
		}
	}

	mcpServerEndpoint := os.Getenv("NEO4J_MCP_ENDPOINT")
	if mcpServerEndpoint != "" {
		return mcpServerEndpoint
	}

	return defaultNeo4jMCPEndpoint
}

func resolveGwsvcListenAddress() string {
	configMu.RLock()
	defer configMu.RUnlock()
	if config.GwsvcListenAddr != "" {
		return config.GwsvcListenAddr
	}
	return defaultGwsvcListenAddr
}

func reloadConfigFromDisk() error {
	configFile := os.Getenv("GWSVC_CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	b, err := os.ReadFile(configFile)
	if err != nil {
		config = gatewayConfig{GwsvcListenAddr: defaultGwsvcListenAddr}
		return err
	}

	var cfg gatewayConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		config = gatewayConfig{GwsvcListenAddr: defaultGwsvcListenAddr}
		return err
	}

	if cfg.GwsvcListenAddr == "" {
		cfg.GwsvcListenAddr = defaultGwsvcListenAddr
	}
	if cfg.Neo4jMCPURL == "" {
		if endpoint := os.Getenv("NEO4J_MCP_ENDPOINT"); endpoint != "" {
			cfg.Neo4jMCPURL = endpoint
		} else {
			cfg.Neo4jMCPURL = defaultNeo4jMCPEndpoint
		}
	}

	configMu.Lock()
	defer configMu.Unlock()
	config = cfg

	return nil
}

func handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := reloadConfigFromDisk(); err != nil {
		http.Error(w, fmt.Sprintf("failed to reload config: %v", err), http.StatusInternalServerError)
		return
	}

	configMu.RLock()
	defer configMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":          "reloaded",
		"gwsvcListenAddr": config.GwsvcListenAddr,
		"neo4jMcpUrl":     config.Neo4jMCPURL,
	})
}

func handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "shutting_down",
	})

	go func() {
		time.Sleep(100 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if httpServer != nil {
			httpServer.Shutdown(ctx)
		}
	}()
}
