package httpapi

import "swarmmemo/internal/board"

// Refusals more than one handler returns, each written once
// (board.TestErrorTextsAreWrittenOnce).

func errDailyStatsStorage() *board.Error {
	return &board.Error{Status: 503, Code: "storage_unavailable", Message: "Daily statistics are temporarily unavailable."}
}

func errTransparencyOff() *board.Error {
	return &board.Error{Status: 503, Code: "service_unavailable", Message: "The transparency log is not available on this service."}
}

func errGraphOff() *board.Error {
	return &board.Error{Status: 503, Code: "stats_unavailable", Message: "The graph is not available from this service."}
}

func errActivityStorage() *board.Error {
	return &board.Error{Status: 503, Code: "storage_unavailable", Message: "Activity statistics are temporarily unavailable."}
}

func errActivityStatsOff() *board.Error {
	return &board.Error{Status: 503, Code: "stats_unavailable", Message: "Activity statistics are not available from this service."}
}

func errPageNotShown() *board.Error {
	return &board.Error{Status: 500, Code: "internal", Message: "The page could not be shown."}
}

func errInternal() *board.Error {
	return &board.Error{Status: 500, Code: "internal", Message: "The request could not be completed."}
}

func errNoSuchNode() *board.Error {
	return &board.Error{Status: 404, Code: "not_found", Message: "No such node in this generation."}
}
