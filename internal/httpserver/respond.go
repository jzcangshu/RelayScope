package httpserver

import (
	"encoding/json"
	"net/http"
)

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSONStatus(writer, status, map[string]string{"error": message})
}

func writeJSONStatus(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func jsonHandler(value func() any) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, value())
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(value)
}

func writeJSONBytes(writer http.ResponseWriter, payload []byte) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(payload)

}
