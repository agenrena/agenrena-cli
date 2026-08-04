package agentbridge

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const routePrefix = "v1."

type Route struct {
	ChatID         string `json:"chat_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Source         string `json:"source,omitempty"`
	Version        int    `json:"v"`
}

func EncodeRoute(route Route) (string, error) {
	route.Version = 1
	route.ChatID = strings.TrimSpace(route.ChatID)
	route.ConversationID = strings.TrimSpace(route.ConversationID)
	route.Source = strings.TrimSpace(route.Source)
	if err := validateRoute(route); err != nil {
		return "", err
	}
	raw, err := json.Marshal(route)
	if err != nil {
		return "", wrapBridgeError("ROUTE_INVALID", "could not encode the message route", false, err)
	}
	return routePrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeRoute(value string) (Route, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, routePrefix) || len(value) > 4096 {
		return Route{}, bridgeError("ROUTE_INVALID", "route is malformed or unsupported", false)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, routePrefix))
	if err != nil {
		return Route{}, bridgeError("ROUTE_INVALID", "route is malformed or unsupported", false)
	}
	var route Route
	if json.Unmarshal(raw, &route) != nil || validateRoute(route) != nil {
		return Route{}, bridgeError("ROUTE_INVALID", "route is malformed or unsupported", false)
	}
	canonical, _ := json.Marshal(route)
	if base64.RawURLEncoding.EncodeToString(canonical) != strings.TrimPrefix(value, routePrefix) {
		return Route{}, bridgeError("ROUTE_INVALID", "route is not canonically encoded", false)
	}
	return route, nil
}

func validateRoute(route Route) error {
	if route.Version != 1 {
		return bridgeError("ROUTE_INVALID", "route version is unsupported", false)
	}
	if route.ChatID != strings.TrimSpace(route.ChatID) ||
		route.ConversationID != strings.TrimSpace(route.ConversationID) ||
		route.Source != strings.TrimSpace(route.Source) {
		return bridgeError("ROUTE_INVALID", "route fields must not contain surrounding whitespace", false)
	}
	if route.ConversationID == "" && (route.Source == "" || route.ChatID == "") {
		return bridgeError("ROUTE_INVALID", "route does not contain an Agenrena destination", false)
	}
	if route.ChatID != "" && route.Source == "" {
		return bridgeError("ROUTE_INVALID", "route source and chat_id must be provided together", false)
	}
	for _, value := range []string{route.ChatID, route.ConversationID, route.Source} {
		if len(value) > 1024 {
			return bridgeError("ROUTE_INVALID", "route contains an oversized field", false)
		}
	}
	return nil
}
