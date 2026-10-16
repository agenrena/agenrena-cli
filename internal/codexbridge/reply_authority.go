package codexbridge

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/agenrena/agenrena-cli/internal/agentbridge"
)

func stableOperationID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	digest[6] = (digest[6] & 0x0f) | 0x40
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", digest[:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}

func replyErrorCode(err error) string {
	var rpc *agentbridge.RPCError
	if errors.As(err, &rpc) {
		return rpc.Code
	}
	return "INTERNAL_ERROR"
}

func replyAuthorityLost(err error) bool {
	switch replyErrorCode(err) {
	case "CONVERSATION_AGENT_REPLY_DISABLED", "NOT_CONVERSATION_MEMBER":
		return true
	default:
		return false
	}
}

func (service *bridgeService) discardRevokedReply(reply Reply, err error) (bool, error) {
	if !replyAuthorityLost(err) {
		return false, nil
	}
	log.Printf("message %s discarded after authority rejection %s", reply.InboundMessageID, replyErrorCode(err))
	return true, service.store.CompleteWithoutReply(reply.InboundMessageID, reply.Route, reply.ThreadID)
}
