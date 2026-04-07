package cfgmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5"
)

type McpgwConfig struct {
	Gateway_Name string
	Gateway_Port int
}

func (m McpgwConfig) GetExistingPort(ctx context.Context) (int, error) {
	conn := dbInitialize()
	defer conn.Close(ctx)
	var port int
	err := conn.QueryRow(ctx, `
		SELECT gateway_svc_port FROM mcpgwbasic WHERE gateway_name = $1;
	`, m.Gateway_Name).Scan(&port)
	if err != nil {
		return 0, err
	}
	return port, nil
}

func (m McpgwConfig) AddConfig(ctx context.Context) error {
	conn := dbInitialize()
	defer conn.Close(ctx)
	_, err := conn.Exec(ctx, `
		INSERT INTO mcpgwbasic (gateway_name, gateway_svc_port) VALUES ($1, $2)
		ON CONFLICT (gateway_name) DO UPDATE SET gateway_svc_port = EXCLUDED.gateway_svc_port;
	`, m.Gateway_Name, m.Gateway_Port)
	if err != nil {
		return err
	}
	fmt.Println("GW config has been added/updated")
	return nil
}

func (m McpgwConfig) TriggerCfgUpdater(oldPort int) error {
	log.Printf("triggering cfgUpdater old port %d -> new port %d", oldPort, m.Gateway_Port)
	cfgUpdaterURL := os.Getenv("CFG_UPDATER_URL")
	if cfgUpdaterURL == "" {
		cfgUpdaterURL = "http://localhost:9091"
	}

	type updatePayload struct {
		OldGwsvcListenPort int `json:"oldGwsvcListenPort"`
		NewGwsvcListenPort int `json:"newGwsvcListenPort"`
	}

	payload := updatePayload{OldGwsvcListenPort: oldPort, NewGwsvcListenPort: m.Gateway_Port}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := http.Post(cfgUpdaterURL+"/update-config", "application/json", bytes.NewReader(jsonBody)) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cfgUpdater returned status %d", resp.StatusCode)
	}

	log.Printf("cfgUpdater successfully triggered port change to %d", m.Gateway_Port)
	return nil
}

func dbInitialize() pgx.Conn {
	cfg := PostgresqlConfig{
		Host:     os.Getenv("DBHOST"),
		Port:     os.Getenv("DBPORT"),
		User:     os.Getenv("DBUSER"),
		Password: os.Getenv("DBPASSWORD"),
		Database: os.Getenv("DBNAME"),
		SSLMode:  os.Getenv("SSLMode"),
	}
	conn, err := pgx.Connect(context.Background(), cfg.String())
	if err != nil {
		panic(err)
	}
	return *conn
}
