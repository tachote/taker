//go:build ignore

// Package upload stores files that users attach to support tickets.
//
// Frozen fixture for the README screenshots: assets/regen.sh renders it, so
// edit it only together with the images. The build tag keeps this demo
// handler out of the module's packages and out of go vet, staticcheck and
// govulncheck.
package upload

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const storageRoot = "/var/lib/tickets/attachments"

type attachment struct {
	Name     string    `json:"name"`
	Size     int       `json:"size"`
	Uploaded time.Time `json:"uploaded"`
}

// Handler accepts PUT /tickets/{id}/attachments?name=<file>.
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ticket := r.PathValue("id")
	name := r.URL.Query().Get("name")
	if ticket == "" || name == "" {
		http.Error(w, "missing ticket or name", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}

	dir := filepath.Join(storageRoot, ticket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	dest := filepath.Join(dir, name)
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		http.Error(w, "write failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(attachment{Name: name, Size: len(body), Uploaded: time.Now()})
}
