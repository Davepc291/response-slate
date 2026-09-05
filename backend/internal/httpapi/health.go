package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
)

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(healthResponse{
		Status:  "ok",
		Service: "greenwich-fire-responder-api",
	}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
