package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"relayscope/internal/routing"
	"relayscope/internal/store"
)

func registerRoutingObservations(mux *http.ServeMux, options Options) {
	mux.HandleFunc("GET /api/v1/public/routing-observations", func(writer http.ResponseWriter, request *http.Request) {
		query, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid routing observation query")
			return
		}
		for key := range query {
			if key != "siteId" {
				writeError(writer, http.StatusBadRequest, "unsupported routing observation query")
				return
			}
		}
		scope := query["siteId"]
		if len(scope) > routing.MaxSites {
			writeError(writer, http.StatusBadRequest, "routing observation site scope exceeds limit")
			return
		}
		ids := make([]int64, 0, len(scope))
		seen := make(map[int64]bool)
		for _, raw := range scope {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw || seen[id] {
				writeError(writer, http.StatusBadRequest, "invalid routing observation site scope")
				return
			}
			seen[id] = true
			ids = append(ids, id)
		}
		envelope, err := options.Store.QueryRoutingObservations(request.Context(), ids, options.Now())
		if errors.Is(err, store.ErrRoutingScope) {
			writeError(writer, http.StatusBadRequest, "routing observation site scope is unavailable")
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "query routing observations")
			return
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "encode routing observations")
			return
		}
		if len(payload) > routing.MaxBytes {
			writeError(writer, http.StatusRequestEntityTooLarge, "routing observation response exceeds limit")
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writeJSONBytes(writer, payload)
	})
}
