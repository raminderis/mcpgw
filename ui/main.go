package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type GatewayConfig struct {
	GatewayName   string
	ListenAddress string
	TargetURL     string
	APIKey        string
	LogLevel      string
}

var currentConfig GatewayConfig

func main() {
	r := gin.Default()
	r.LoadHTMLGlob("templates/*")

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{"Message": ""})
	})

	r.POST("/configure", func(c *gin.Context) {
		currentConfig = GatewayConfig{
			GatewayName:   c.PostForm("gatewayName"),
			ListenAddress: c.PostForm("listenAddress"),
			TargetURL:     c.PostForm("targetUrl"),
			APIKey:        c.PostForm("apiKey"),
			LogLevel:      c.PostForm("logLevel"),
		}

		cfgmgrUrl := os.Getenv("CFGMGR_URL")
		if cfgmgrUrl == "" {
			cfgmgrUrl = "http://127.0.0.1:8091/addgwconfig"
		}
		port := parsePort(currentConfig.ListenAddress)
		body := map[string]any{
			"mcpgwname": currentConfig.GatewayName,
			"port":      port,
		}
		jsonBytes, _ := json.Marshal(body)
		resp, err := http.Post(cfgmgrUrl, "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			fmt.Println("couldnt save the gw config")
			c.HTML(http.StatusBadGateway, "index.html", gin.H{"Message": "Could not save config to cfgmgr"})
			return
		}
		defer resp.Body.Close()
		message := fmt.Sprintf("Configuration saved for %s -> %s", currentConfig.GatewayName, currentConfig.TargetURL)
		c.HTML(http.StatusOK, "index.html", gin.H{"Message": message})
	})

	if err := r.Run(":8090"); err != nil {
		panic(err)
	}
}

// parsePort extracts an integer port from strings like ":9090", "9090", or "localhost:9090".
func parsePort(addr string) int {
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[idx+1:]
	}
	p, err := strconv.Atoi(strings.TrimSpace(addr))
	if err != nil || p < 1 || p > 65535 {
		return 9090
	}
	return p
}
