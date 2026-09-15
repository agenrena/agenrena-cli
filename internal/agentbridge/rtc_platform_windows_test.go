//go:build windows

package agentbridge

import (
	"context"
	"testing"
)

func TestRTCHelperManagerRejectsCallsOnWindows(t *testing.T) {
	manager := NewRTCHelperManager(RTCHelperManagerConfig{})
	_, err := manager.Accept(context.Background(), AcceptCallParams{CallID: "call-1"})
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "RTC_HELPER_UNAVAILABLE" {
		t.Fatalf("err=%v", err)
	}
}
