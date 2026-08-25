package warp

import (
	"errors"
	"strings"
)

type requestError struct {
	conversationID string
	requestID      string
	err            error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }
func (e *requestError) WarpRequestID() string {
	return e.requestID
}
func (e *requestError) WarpConversationID() string {
	return e.conversationID
}

func AttachRequestMetadata(err error, conversationID, requestID string) error {
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if err == nil || requestID == "" {
		return err
	}
	return &requestError{conversationID: conversationID, requestID: requestID, err: err}
}

func ConversationIDFromError(err error) string {
	var identified interface{ WarpConversationID() string }
	if errors.As(err, &identified) {
		return strings.TrimSpace(identified.WarpConversationID())
	}
	return ""
}

func RequestIDFromError(err error) string {
	var identified interface{ WarpRequestID() string }
	if errors.As(err, &identified) {
		return strings.TrimSpace(identified.WarpRequestID())
	}
	return ""
}
