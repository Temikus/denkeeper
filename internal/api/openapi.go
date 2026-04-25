package api

import (
	"embed"
	"net/http"
)

//go:embed docs/swagger.json
var openapiFS embed.FS

// handleOpenAPISpec serves the generated OpenAPI/Swagger spec.
//
// @Summary OpenAPI specification
// @Description Returns the generated OpenAPI 2.0 (Swagger) specification
// @Tags meta
// @Produce json
// @Success 200 {object} map[string]any
// @Router /openapi.json [get]
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	data, err := openapiFS.ReadFile("docs/swagger.json")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "OpenAPI spec not available",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
}
