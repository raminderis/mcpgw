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
	"sync"
	"time"

	"cutmenot.ai/mcpgw/gwsvc/cmd/neo4jClient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultGwsvcListenAddr  = ":9090"
	defaultNeo4jMCPEndpoint = "http://localhost:9094/mcp"
	defaultAISecVerifyURL   = "http://aisec:9097/verify"
	defaultPolicyDecideURL  = "http://aipolicy:9099/decide"
	defaultMCPServiceName   = "neo4j-mcp"
	defaultGatewayEnv       = "prod"
	defaultConfigFile       = "./gwsvc-config.json"
	defaultAIRegistryURL    = "http://airegistry:9098/registries/by-name"
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

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

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
			TargetService  string         `json:"target_service"`
			TargetUsername string         `json:"target_username"`
			TargetPassword string         `json:"target_password"`
			MCPBody        map[string]any `json:"mcp_body"`
		}
		json.Unmarshal(body, &rpc)
		if method, ok := rpc.MCPBody["method"].(string); ok && method == "initialize" {
			fmt.Println("Intercepted initialize -> forwarding to external mcp")
			externalMCPResp, _ := forwardToExternalMCP(
				rpc.TargetService,
				rpc.TargetUsername,
				rpc.TargetPassword,
				rpc.MCPBody,
			)
			respBytes, err := json.Marshal(externalMCPResp)
			if err != nil {
				http.Error(w, "failed to marshal mcp response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(respBytes)
			return
		}
		if method, ok := rpc.MCPBody["method"].(string); ok && method == "notifications/initialized" {
			fmt.Println("Intercepted notifications/initialized -> forwarding it to neo4j mcp")
			externalMCPResp, _ := forwardToExternalMCP(
				rpc.TargetService,
				rpc.TargetUsername,
				rpc.TargetPassword,
				rpc.MCPBody,
			)
			respBytes, err := json.Marshal(externalMCPResp)
			if err != nil {
				http.Error(w, "failed to marshal mcp response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(respBytes)
			return
		}
		if method, ok := rpc.MCPBody["method"].(string); ok && method == "tools/list" {
			fmt.Println("Intercepted tools/list -> forwarding it to neo4j mcp")
			externalMCPResp := forwardToolsListTotargetService(
				rpc.TargetService,
				rpc.TargetUsername,
				rpc.TargetPassword,
				rpc.MCPBody,
			)
			respBytes, err := json.Marshal(externalMCPResp)
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
		Handler: authMiddleware(policyMiddleware(http.DefaultServeMux)),
	}

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("error is %v", err)
	}
}

func forwardToExternalMCP(mcpServer, mcpUsername, mcpPassword string, mcpPayload map[string]any) (JSONRPCResponse, error) {
	client := &http.Client{}
	b, _ := json.Marshal(mcpPayload)
	fmt.Println("Sent:", string(b))
	req, _ := http.NewRequest("POST", mcpServer, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	neo4jUser := mcpUsername
	neo4jPass := mcpPassword
	req.SetBasicAuth(neo4jUser, neo4jPass)
	resp, err := client.Do(req)
	if err != nil {
		return JSONRPCResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return JSONRPCResponse{}, err
	}
	fmt.Println("STATUS:", resp.Status)
	fmt.Println("RESULT:", string(body))
	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return JSONRPCResponse{}, err
	}

	return rpcResp, nil
}

func forwardToolsListTotargetServiceGeneric(mcpServer, mcpUsername, mcpPassword, sessionID string, mcpPayload map[string]any) string {
	client := &http.Client{}
	sessionID, _ = neo4jClient.PostRPC(client, mcpServer, sessionID, mcpUsername, mcpPassword, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  mcpPayload["method"],
		"params":  map[string]any{},
	})
	fmt.Println("forward tools generic session id: ", sessionID)
	return sessionID
}

func forwardToolsListTotargetService(mcpServer, mcpUsername, mcpPassword string, mcpPayload map[string]any) neo4jClient.ToolsListResult {
	client := &http.Client{}
	var sessionID string
	sessionID, _ = neo4jClient.PostRPC(client, mcpServer, sessionID, mcpUsername, mcpPassword, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})

	neo4jClient.PostRPC(client, mcpServer, sessionID, mcpUsername, mcpPassword, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	_, tools := neo4jClient.PostRPC(client, mcpServer, sessionID, mcpUsername, mcpPassword, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	return tools
}

/*
func forwardToolsListToNeo4jRaw(body []byte) neo4jClient.ToolsListResult {
	// Fetch registry info for neo4j-mcp
	registryInfo, err := fetchRegistryInfo(defaultMCPServiceName)
	if err != nil {
		log.Printf("failed to fetch registry info: %v", err)
		return neo4jClient.ToolsListResult{}
	}

	mcpServerEndpoint := registryInfo.Endpoint
	client := &http.Client{}
	var sessionID string
	sessionID, _ = neo4jClient.PostRPC(client, mcpServerEndpoint, sessionID, registryInfo.Username, registryInfo.Password, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})

	neo4jClient.PostRPC(client, mcpServerEndpoint, sessionID, registryInfo.Username, registryInfo.Password, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	_, tools := neo4jClient.PostRPC(client, mcpServerEndpoint, sessionID, registryInfo.Username, registryInfo.Password, map[string]any{
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
*/

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

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
			return
		}

		ok, err := verifyTokenWithAISec(r.Context(), strings.TrimSpace(parts[1]))
		if err != nil {
			http.Error(w, "authentication service unavailable "+err.Error(), http.StatusUnauthorized)
			return
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func verifyTokenWithAISec(ctx context.Context, token string) (bool, error) {
	verifyURL := os.Getenv("AISEC_VERIFY_URL")
	if verifyURL == "" {
		verifyURL = defaultAISecVerifyURL
	}

	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, verifyURL, nil)
	if err != nil {
		return false, err
	}
	q := req.URL.Query()
	q.Set("authCode", token)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var payload struct {
		Valid *bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		// Backward compatible with services that only return HTTP 200/401 and no body.
		return true, nil
	}

	if payload.Valid == nil {
		return true, nil
	}

	return *payload.Valid, nil
}

func policyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only enforce policy on the /mcp endpoint
		if r.URL.Path != "/mcp" {
			next.ServeHTTP(w, r)
			return
		}

		// Read and restore body so downstream handlers can still read it
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		var rpc struct {
			TargetService  string         `json:"target_service"`
			TargetUsername string         `json:"target_username"`
			TargetPassword string         `json:"target_password"`
			MCPBody        map[string]any `json:"mcp_body"`
		}
		json.Unmarshal(body, &rpc)

		env := os.Getenv("GATEWAY_ENV")
		if env == "" {
			env = defaultGatewayEnv
		}

		if method, ok := rpc.MCPBody["method"].(string); ok && method != "" {
			allowed, err := checkPolicyDecision(r.Context(), defaultMCPServiceName, method, env)
			if err != nil {
				log.Printf("policy service unavailable: %v", err)
				http.Error(w, "policy service unavailable", http.StatusForbidden)
				return
			}
			if !allowed {
				log.Printf("policy denied: remote_mcp_service=%s resource_access_request=%s environment=%s",
					defaultMCPServiceName, method, env)
				http.Error(w, "policy denied", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		}

	})
}

func checkPolicyDecision(ctx context.Context, remoteMCPService, resourceAccessRequest, environment string) (bool, error) {
	decideURL := os.Getenv("POLICYMGR_DECIDE_URL")
	if decideURL == "" {
		decideURL = defaultPolicyDecideURL
	}

	reqBody, err := json.Marshal(map[string]string{
		"remote_mcp_service":      remoteMCPService,
		"resource_access_request": resourceAccessRequest,
		"environment":             environment,
	})
	if err != nil {
		return false, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, decideURL, bytes.NewReader(reqBody))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var payload struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, err
	}

	return payload.Allowed, nil
}
