package cfgmgr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

func GwconfigHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("gwconfighandler from the config process")
	var payload struct {
		McpgwConfig string `json:"mcpgwname"`
		Port        int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if payload.Port < 1 || payload.Port > 65535 {
		http.Error(w, "port must be between 1 and 65535", http.StatusBadRequest)
		return
	}

	mcpConfig := McpgwConfig{
		Gateway_Name: payload.McpgwConfig,
		Gateway_Port: payload.Port,
	}

	// Fetch old port BEFORE upsert so comparison is valid
	oldPort, _ := mcpConfig.GetExistingPort(r.Context())

	fmt.Println("attempting to add config into PG.")
	if err := mcpConfig.AddConfig(r.Context()); err != nil {
		log.Printf("error adding config: %v", err)
		http.Error(w, "failed to add config", http.StatusInternalServerError)
		return
	}

	if oldPort != mcpConfig.Gateway_Port {
		if err := mcpConfig.TriggerCfgUpdater(oldPort); err != nil {
			log.Printf("warning: failed to trigger cfgUpdater: %v", err)
		}
	} else {
		log.Printf("port unchanged (%d), no restart needed", mcpConfig.Gateway_Port)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"name":   payload.McpgwConfig,
		"port":   payload.Port,
	})
}
