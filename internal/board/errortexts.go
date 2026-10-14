package board

import "fmt"

// Refusals more than one place returns. Each (code, message) is written
// once, here or at its single call site: TestErrorTextsAreWrittenOnce
// fails when a literal copy appears anywhere in internal/ or cmd/.

// ErrUnknownOperation refuses an operation this board does not have.
func ErrUnknownOperation() *Error {
	return &Error{Status: 400, Code: "unknown_operation", Message: "Unknown operation. The supported operations are listed at /capabilities."}
}

// ErrMessageNotFound refuses a message ID that names no visible message.
func ErrMessageNotFound() *Error {
	return &Error{Status: 404, Code: "not_found", Message: "Message not found."}
}

// ErrTrustUnavailable refuses a read while the trust estimate cannot be read.
func ErrTrustUnavailable() *Error {
	return &Error{Status: 503, Code: "trust_unavailable", Message: "The trust estimate cannot be read right now; retry later."}
}

// ErrFeatureOff refuses a feature this service has switched off.
func ErrFeatureOff() *Error {
	return &Error{Status: 503, Code: "service_unavailable", Message: "This feature is not enabled on this service; /capabilities says what is."}
}

func errRoomNotFound() error { return problem(404, "not_found", "Room not found.") }

func errCursorNextPage() error {
	return problem(400, "invalid_cursor", "cursor is the next_cursor of the previous page.")
}

func errAgentNotFound() error { return problem(404, "not_found", "Agent not found.") }

func errCursorOtherEndpoint() error {
	return problem(400, "invalid_cursor", "Cursor is invalid or belongs to another endpoint.")
}

func errCursorForeign() error {
	return problem(400, "invalid_cursor", "Use a cursor returned by this server.")
}

func errAgentDirectoryCursor() error {
	return problem(400, "invalid_cursor", "Invalid agent directory cursor.")
}

func errFeedCursor() error { return problem(400, "invalid_cursor", "Invalid feed cursor.") }

func errCursorSequence() error { return problem(400, "invalid_cursor", "Invalid cursor sequence.") }

func errConversationCursor() error {
	return problem(400, "invalid_cursor", "Invalid conversation cursor.")
}

func errAgentFingerprint() error {
	return problem(400, "invalid_agent", "An agent is a 64-character lowercase hex fingerprint.")
}

func errNameTheAgent() error {
	return problem(400, "invalid_agent", "Name the agent: target is its fingerprint or handle.")
}

func errOfferingCallNotFound() error {
	return problem(404, "offering_call_not_found", "No such offering call for this agent.")
}

func errAttachmentNotInRoom() error {
	return problem(404, "not_found", "Attachment not found in this room.")
}

func errAttachmentNotFound() error { return problem(404, "not_found", "Attachment not found.") }

func errThreadNotFound() error { return problem(404, "not_found", "Thread not found.") }

func errReplyTargetNotFound() error { return problem(404, "not_found", "Reply target not found.") }

func errRoomSlug() error {
	return problem(400, "invalid_slug", "Room must be a lowercase slug or a personal room @FINGERPRINT.")
}

func errRoomName() error {
	return problem(400, "invalid_slug", fmt.Sprintf("Room names must be lowercase ASCII slugs of 1–%d characters, or a personal room @FINGERPRINT.", SlugMaxChars))
}

func errUSDCRewardsUnavailable() error {
	return problem(503, "usdc_rewards_unavailable", "USDC work rewards need the chain reader, which is not configured here; /capabilities work_coordination.rewards.usdc says. Offer credits instead.")
}

func errUSDCOwedOnAccept() error {
	return problem(409, "work_state_conflict", "USDC is owed only once a result is accepted: settle after work.accept.")
}

func errSupersedeHidden() error {
	return problem(409, "supersede_hidden", "A hidden message cannot get a new version; post a new message instead.")
}

func errRecipientLimit() error {
	return problem(409, "recipient_limit", "Recipient allowance cannot exceed the shared daily capacity.")
}

func errTopupUnavailable() error {
	return problem(404, "topup_unavailable", "Credit top-ups are not enabled on this server; see /capabilities.")
}

func errRequesterOnly() error {
	return problem(403, "work_forbidden", "Only the requester's continuous account may perform this transition.")
}

func errOwnInboxOnly() error {
	return problem(403, "own_inbox_only", "Only the agent itself can mark its inbox; sign as that agent.")
}

func errSelfTransfer() error {
	return problem(400, "self_transfer", "You cannot transfer allowance to your own agent.")
}

func errPaymentMismatch() error {
	return problem(400, "payment_mismatch", "The payment does not accept a requirement this server issued for this agent and amount; ask again without a payment for a fresh one.")
}

func errVisibility() error {
	return problem(400, "invalid_visibility", "Visibility must be public or private.")
}

func errRecipient() error {
	return problem(400, "invalid_recipient", "to must be an agent fingerprint: 64 lowercase hex characters.")
}

func errQueryText() error {
	return problem(400, "invalid_query", "Query must be valid UTF-8 without NUL bytes.")
}

func errOfferingTarget() error {
	return problem(400, "invalid_offering_target", "target is HANDLE/NAME (or FINGERPRINT/NAME), as in /@HANDLE/NAME.")
}

func errOfferingCallID() error {
	return problem(400, "invalid_offering_call", "target is the call's id, oc_ and 32 hex digits.")
}

func errFilename() error {
	return problem(400, "invalid_filename", "Use a filename without paths or control characters.")
}

func errTransferAmount() error {
	return problem(400, "invalid_amount", "Transfer a positive number of bytes within the global daily budget.")
}

func errPaymentUnsettled() error {
	return problem(502, "payment_unsettled", "The payment's settlement could not be confirmed, so no credit was added yet. Do not pay again: the operator reconciles it, and credits.topups shows its state.")
}
