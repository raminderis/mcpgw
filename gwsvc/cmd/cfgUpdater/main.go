package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/joho/godotenv"
)

const (
	defaultUpdaterPort = ":9091"
	defaultConfigFile  = "./gwsvc-config.json"
	defaultGwsvcPort   = 9090
)

type updateRequest struct {
	GwsvcListenPort    int `json:"gwsvcListenPort"`
	OldGwsvcListenPort int `json:"oldGwsvcListenPort"`
	NewGwsvcListenPort int `json:"newGwsvcListenPort"`
}

type updaterState struct {
	mu         sync.Mutex
	configFile string
	dbConnStr  string
}

type gatewayConfig struct {
	GwsvcListenAddr string `json:"gwsvcListenAddr"`
}

func main() {
	_ = godotenv.Load()

	// Build database connection string
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "5432"
	}
	dbUser := os.Getenv("DB_USER")
	if dbUser == "" {
		dbUser = "cfgmgr"
	}
	dbPassword := os.Getenv("DB_PASSWORD")
	if dbPassword == "" {
		dbPassword = "cfgmgrpass"
	}
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "cfgmgr"
	}

	dbConnStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, dbHost, dbPort, dbName)

	state := &updaterState{
		configFile: resolveConfigFile(),
		dbConnStr:  dbConnStr,
	}
	if err := state.ensureConfigFile(); err != nil {
		log.Fatalf("failed to initialize config: %v", err)
	}

	http.HandleFunc("/health", state.handleHealth)
	http.HandleFunc("/update-config", state.handleUpdateConfig)

	listenAddr := os.Getenv("CFG_UPDATER_PORT")
	if listenAddr == "" {
		listenAddr = defaultUpdaterPort
	}

	log.Printf("cfgUpdater listening on %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("cfgUpdater stopped: %v", err)
	}
}

func (s *updaterState) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, _ := readConfig(s.configFile)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"configFile":      s.configFile,
		"gwsvcListenAddr": cfg.GwsvcListenAddr,
	})
}

func (s *updaterState) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := readConfig(s.configFile)
	if err != nil {
		cfg = gatewayConfig{}
	}

	oldPort := req.OldGwsvcListenPort
	newPort := req.NewGwsvcListenPort
	if req.GwsvcListenPort > 0 {
		// Backward compatibility for older cfgmgr payloads.
		newPort = req.GwsvcListenPort
		oldPort = parsePortFromAddr(cfg.GwsvcListenAddr)
	}

	if oldPort < 1 || oldPort > 65535 {
		http.Error(w, "oldGwsvcListenPort must be between 1 and 65535", http.StatusBadRequest)
		return
	}
	if newPort < 1 || newPort > 65535 {
		http.Error(w, "newGwsvcListenPort must be between 1 and 65535", http.StatusBadRequest)
		return
	}

	log.Printf("received update-config oldPort=%d newPort=%d", oldPort, newPort)

	newAddr := fmt.Sprintf(":%d", newPort)
	if cfg.GwsvcListenAddr == newAddr {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "no_change",
			"gwsvcListenAddr": cfg.GwsvcListenAddr,
		})
		return
	}

	cfg.GwsvcListenAddr = newAddr
	if err := writeConfigAtomic(s.configFile, cfg); err != nil {
		http.Error(w, fmt.Sprintf("failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

	_ = s.triggerGwsvcReset(oldPort)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "updated",
		"gwsvcListenAddr": cfg.GwsvcListenAddr,
	})
}

func parsePortFromAddr(addr string) int {
	var p int
	if _, err := fmt.Sscanf(addr, ":%d", &p); err != nil {
		return defaultGwsvcPort
	}
	if p < 1 || p > 65535 {
		return defaultGwsvcPort
	}
	return p
}

func (s *updaterState) ensureConfigFile() error {
	if _, err := os.Stat(s.configFile); err == nil {
		return nil
	}
	initial := gatewayConfig{GwsvcListenAddr: fmt.Sprintf(":%d", defaultGwsvcPort)}
	return writeConfigAtomic(s.configFile, initial)
}

func resolveConfigFile() string {
	configFile := os.Getenv("GWSVC_CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}
	return configFile
}

func writeConfigAtomic(path string, cfg gatewayConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readConfig(path string) (gatewayConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return gatewayConfig{}, err
	}
	var cfg gatewayConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return gatewayConfig{}, err
	}
	if cfg.GwsvcListenAddr == "" {
		cfg.GwsvcListenAddr = fmt.Sprintf(":%d", defaultGwsvcPort)
	}
	return cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *updaterState) triggerGwsvcReset(oldPort int) error {
	gwsvcURL := fmt.Sprintf("http://gwsvc:%d", oldPort)
	resp, err := http.Post(gwsvcURL+"/shutdown", "application/json", nil)
	if err != nil {
		log.Printf("warning: failed to trigger gwsvc shutdown at old port %d (%s): %v", oldPort, gwsvcURL, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("warning: gwsvc shutdown at %s returned status %d", gwsvcURL, resp.StatusCode)
		return fmt.Errorf("gwsvc returned status %d", resp.StatusCode)
	}
	log.Printf("gwsvc shutdown triggered successfully at %s (orchestrator will restart it)", gwsvcURL)
	return nil
}
