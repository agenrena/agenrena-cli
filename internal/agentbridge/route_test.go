package agentbridge

import "testing"

func TestRouteEncodingMatchesProtocolFixture(t *testing.T) {
	route := Route{
		ChatID: "chat_456", ConversationID: "conv_456", Source: "agenrena", Version: 1,
	}
	encoded, err := EncodeRoute(route)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "v1.eyJjaGF0X2lkIjoiY2hhdF80NTYiLCJjb252ZXJzYXRpb25faWQiOiJjb252XzQ1NiIsInNvdXJjZSI6ImFnZW5yZW5hIiwidiI6MX0"
	if encoded != expected {
		t.Fatalf("route=%q want=%q", encoded, expected)
	}
	decoded, err := DecodeRoute(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != route {
		t.Fatalf("decoded=%+v want=%+v", decoded, route)
	}
}

func TestRouteSupportsConversationOnlyAndRejectsNonCanonicalValue(t *testing.T) {
	encoded, err := EncodeRoute(Route{ConversationID: "conv", Source: "agenrena"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRoute(encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRoute(encoded + "="); err == nil {
		t.Fatal("expected padded route to be rejected")
	}
}
