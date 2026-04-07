package main

import (
	"fmt"
	"log"
	"net/http"

	"cutmenot.ai/mcpgw/cfgmgr/internal/cfgmgr"
	"github.com/go-chi/chi"
	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file", err)
	}
	router := chi.NewRouter()
	router.Post("/addgwconfig", cfgmgr.GwconfigHandler)
	fmt.Println("CFGMGR is listening on :8091")
	http.ListenAndServe("127.0.0.1:8091", router)
}
