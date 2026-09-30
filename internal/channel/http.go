package channel

import (
	"net/http"

	"orchids-api/internal/util"
)

type RegistryPayload struct {
	Providers []Definition `json:"providers"`
	Default   ID           `json:"defaultProviderKey"`
}

func Payload() RegistryPayload {
	return RegistryPayload{Providers: All(), Default: Default().ID}
}

func HandleRegistry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	util.WriteJSON(w, Payload())
}
