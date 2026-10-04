// Package routing describes the narrow machine observation contract.
// It does not decide candidate eligibility or perform network requests.
package routing

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
)

const MaxRows = 20_000
const MaxBytes = 10 * 1024 * 1024
const MaxSites = 1000

// Source identities keep their original spelling. Only user confirmation may
// normalize whitespace; display helpers must not silently change match keys.
func SourceString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func SourceStrings(object map[string]any, keys ...string) []string {
	for _, key := range keys {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}
		result := make([]string, 0, len(items))
		for _, item := range items {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				result = append(result, value)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return nil
}

type ProtocolScope struct {
	Kind      string   `json:"kind"`
	Protocols []string `json:"protocols,omitempty"`
}

type Price struct {
	Kind                          string        `json:"kind"`
	Source                        string        `json:"source"`
	MoneyUnit                     *string       `json:"moneyUnit"`
	InputPerMillion               *string       `json:"inputPerMillion"`
	OutputPerMillion              *string       `json:"outputPerMillion"`
	ProtocolScope                 ProtocolScope `json:"protocolScope"`
	IncludesGroupMultiplier       *bool         `json:"includesGroupMultiplier"`
	PriceObservedAt               *int64        `json:"priceObservedAt"`
	SuggestedIntervalMilliseconds *int64        `json:"suggestedIntervalMilliseconds"`
	ValidForMilliseconds          *int64        `json:"validForMilliseconds"`
}

type Availability struct {
	Status                        string        `json:"status"`
	Evidence                      string        `json:"evidence"`
	ProtocolScope                 ProtocolScope `json:"protocolScope"`
	SampleCount                   *int64        `json:"sampleCount"`
	SuccessRate                   *float64      `json:"successRate"`
	WindowStartedAt               *int64        `json:"windowStartedAt"`
	WindowEndedAt                 *int64        `json:"windowEndedAt"`
	SourceObservedAt              *int64        `json:"sourceObservedAt"`
	CollectedAt                   *int64        `json:"collectedAt"`
	CollectionStatus              string        `json:"collectionStatus"`
	SuggestedIntervalMilliseconds *int64        `json:"suggestedIntervalMilliseconds"`
	ValidForMilliseconds          *int64        `json:"validForMilliseconds"`
}

type Observation struct {
	SiteID       string       `json:"siteId"`
	RawModelName string       `json:"rawModelName"`
	RawGroupName *string      `json:"rawGroupName"`
	Availability Availability `json:"availability"`
	Price        *Price       `json:"price"`
}

type Envelope struct {
	SchemaVersion    int           `json:"schemaVersion"`
	SourceInstanceID string        `json:"sourceInstanceId"`
	Revision         string        `json:"revision"`
	GeneratedAt      int64         `json:"generatedAt"`
	RequestedSiteIDs []string      `json:"requestedSiteIds"`
	Observations     []Observation `json:"observations"`
}

// Extension is saved beside the existing display pricing and source fields.
// Absence is legacy data, not evidence that can be inferred or backfilled.
type Extension struct {
	Version      int           `json:"version"`
	GroupKnown   *bool         `json:"groupKnown"`
	Availability *Availability `json:"availability"`
	Price        *Price        `json:"price"`
}

func UnknownAvailability() Availability {
	return Availability{Status: "unknown", Evidence: "unknown", ProtocolScope: ProtocolScope{Kind: "unknown"}, CollectionStatus: "unknown"}
}

func ReadExtension(raw []byte) Extension {
	var wrapper struct {
		Routing Extension `json:"routingObservation"`
	}
	if json.Unmarshal(raw, &wrapper) != nil || wrapper.Routing.Version != 1 {
		return Extension{}
	}
	value := wrapper.Routing
	if value.Price != nil && !ValidPrice(*value.Price) {
		value.Price = nil
	}
	if value.Availability != nil && !validAvailability(*value.Availability) {
		value.Availability = nil
	}
	return value
}

func WriteExtension(raw []byte, value Extension) json.RawMessage {
	fields := make(map[string]json.RawMessage)
	// New collection data only: never patch stored legacy rows to invent facts.
	_ = json.Unmarshal(raw, &fields)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	value.Version = 1
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	fields["routingObservation"] = encoded
	encoded, err = json.Marshal(fields)
	if err != nil {
		return raw
	}
	return encoded
}

var decimal = regexp.MustCompile(`^\d{1,12}(?:\.\d{1,12})?$`)

func validScope(scope ProtocolScope, price bool) bool {
	if scope.Kind == "unknown" {
		return len(scope.Protocols) == 0
	}
	if scope.Kind != "explicit" && !(price && scope.Kind == "all-supported") {
		return false
	}
	if len(scope.Protocols) == 0 || len(scope.Protocols) > 3 {
		return false
	}
	seen := make(map[string]bool)
	for _, protocol := range scope.Protocols {
		if seen[protocol] || (protocol != "openai-completions" && protocol != "openai-responses" && protocol != "anthropic-messages") {
			return false
		}
		seen[protocol] = true
	}
	return true
}

func validTime(value *int64) bool {
	return value == nil || (*value >= 0 && *value <= 9_007_199_254_740_991)
}
func validInterval(value *int64) bool {
	return value == nil || (*value > 0 && *value <= 9_007_199_254_740_991)
}
func validDecimal(value *string) bool { return value == nil || decimal.MatchString(*value) }

func ValidPrice(value Price) bool {
	if value.Kind != "per-token" && value.Kind != "per-request" && value.Kind != "tiered" && value.Kind != "unknown" {
		return false
	}
	if value.Source != "exact-group" && value.Source != "unknown" {
		return false
	}
	return (value.MoneyUnit == nil || strings.TrimSpace(*value.MoneyUnit) != "") && validDecimal(value.InputPerMillion) && validDecimal(value.OutputPerMillion) && validScope(value.ProtocolScope, true) && validTime(value.PriceObservedAt) && validInterval(value.SuggestedIntervalMilliseconds) && validInterval(value.ValidForMilliseconds)
}

func validAvailability(value Availability) bool {
	if value.Status != "available" && value.Status != "unavailable" && value.Status != "unknown" {
		return false
	}
	if value.Evidence != "presence" && value.Evidence != "site-aggregate" && value.Evidence != "group-observed" && value.Evidence != "copied" && value.Evidence != "unknown" {
		return false
	}
	if value.CollectionStatus != "ok" && value.CollectionStatus != "error" && value.CollectionStatus != "unknown" {
		return false
	}
	if value.SampleCount != nil && (*value.SampleCount < 0 || *value.SampleCount > 9_007_199_254_740_991) {
		return false
	}
	if value.SuccessRate != nil && (math.IsNaN(*value.SuccessRate) || *value.SuccessRate < 0 || *value.SuccessRate > 1) {
		return false
	}
	return validScope(value.ProtocolScope, false) && validTime(value.WindowStartedAt) && validTime(value.WindowEndedAt) && validTime(value.SourceObservedAt) && validTime(value.CollectedAt) && validInterval(value.SuggestedIntervalMilliseconds) && validInterval(value.ValidForMilliseconds)
}
