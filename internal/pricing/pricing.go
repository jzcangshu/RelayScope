package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const ExtensionKey = "pricing"

// DisplayPrice is the normalized price shown to users. Exactly one of the
// input/output pair or fixed-per-request is populated for a priced quote.
type DisplayPrice struct {
	Available            bool     `json:"available"`
	Mode                 string   `json:"mode,omitempty"`
	Currency             string   `json:"currency,omitempty"`
	CurrencySymbol       string   `json:"currencySymbol,omitempty"`
	InputPerMillion      *float64 `json:"inputPerMillion,omitempty"`
	OutputPerMillion     *float64 `json:"outputPerMillion,omitempty"`
	CacheReadPerMillion  *float64 `json:"cacheReadPerMillion,omitempty"`
	CacheWritePerMillion *float64 `json:"cacheWritePerMillion,omitempty"`
	FixedPerRequest      *float64 `json:"fixedPerRequest,omitempty"`
	GroupMultiplier      *float64 `json:"groupMultiplier,omitempty"`
}

// ModelPrice stores source-level billing metadata and the normalized group
// quotes. It is intentionally independent from health observations so a new
// pricing decoder can be added without changing the monitor contract.
type ModelPrice struct {
	RawName          string             `json:"rawName"`
	Mode             string             `json:"mode,omitempty"`
	Currency         string             `json:"currency,omitempty"`
	CurrencySymbol   string             `json:"currencySymbol,omitempty"`
	ModelRatio       *float64           `json:"modelRatio,omitempty"`
	ModelPrice       *float64           `json:"modelPrice,omitempty"`
	CompletionRatio  *float64           `json:"completionRatio,omitempty"`
	CacheRatio       *float64           `json:"cacheRatio,omitempty"`
	CacheCreateRatio *float64           `json:"cacheCreateRatio,omitempty"`
	CacheReadPrice   *float64           `json:"cacheReadPrice,omitempty"`
	CacheWritePrice  *float64           `json:"cacheWritePrice,omitempty"`
	QuotaPerUnit     *float64           `json:"quotaPerUnit,omitempty"`
	GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`
	ExchangeRate     float64            `json:"-"`
}

type Catalog struct {
	Models      map[string]ModelPrice
	GroupPrices map[string]map[string]DisplayPrice
}

type Decoder interface {
	Key() string
	Decode(pricingBody, statusBody []byte) (Catalog, error)
}

type Registry struct {
	decoders map[string]Decoder
}

func NewRegistry(decoders ...Decoder) (*Registry, error) {
	registry := &Registry{decoders: make(map[string]Decoder, len(decoders))}
	for _, decoder := range decoders {
		if decoder == nil || strings.TrimSpace(decoder.Key()) == "" {
			return nil, errors.New("pricing decoder and key are required")
		}
		if _, exists := registry.decoders[decoder.Key()]; exists {
			return nil, fmt.Errorf("duplicate pricing decoder %q", decoder.Key())
		}
		registry.decoders[decoder.Key()] = decoder
	}
	return registry, nil
}

func (registry *Registry) Decode(key string, pricingBody, statusBody []byte) (Catalog, error) {
	if registry == nil {
		return Catalog{}, errors.New("pricing registry is nil")
	}
	decoder, ok := registry.decoders[key]
	if !ok {
		return Catalog{}, fmt.Errorf("pricing decoder %q is not registered", key)
	}
	return decoder.Decode(pricingBody, statusBody)
}

func DefaultRegistry() *Registry {
	registry, err := NewRegistry(NewAPIDecoder{}, ModelMarketDecoder{})
	if err != nil {
		panic(err)
	}
	return registry
}

func ModelExtension(model ModelPrice) json.RawMessage {
	payload, err := json.Marshal(map[string]any{ExtensionKey: model})
	if err != nil {
		return nil
	}
	return payload
}

func GroupExtension(price DisplayPrice) json.RawMessage {
	payload, err := json.Marshal(map[string]any{ExtensionKey: price})
	if err != nil {
		return nil
	}
	return payload
}

func PriceFromExtensions(modelExtension, groupExtension []byte) *DisplayPrice {
	var group struct {
		Price *DisplayPrice `json:"pricing"`
	}
	if len(groupExtension) > 0 && json.Unmarshal(groupExtension, &group) == nil && group.Price != nil {
		return group.Price
	}
	var model struct {
		Price ModelPrice `json:"pricing"`
	}
	if len(modelExtension) == 0 || json.Unmarshal(modelExtension, &model) != nil || model.Price.RawName == "" {
		return nil
	}
	pricesByGroup := PricesForModel(model.Price)
	prices := make([]*DisplayPrice, 0, len(pricesByGroup))
	for _, price := range pricesByGroup {
		price := price
		prices = append(prices, &price)
	}
	best := LowestAvailable(prices)
	if best != nil {
		best.GroupMultiplier = nil
	}
	return best
}

func LowestAvailable(prices []*DisplayPrice) *DisplayPrice {
	var best *DisplayPrice
	for _, price := range prices {
		if price == nil || !price.Available {
			continue
		}
		if best == nil || comparableAmount(price) < comparableAmount(best) {
			copy := *price
			best = &copy
		}
	}
	return best
}

func comparableAmount(price *DisplayPrice) float64 {
	if price.FixedPerRequest != nil {
		return *price.FixedPerRequest
	}
	if price.InputPerMillion != nil {
		return *price.InputPerMillion
	}
	return 0
}
