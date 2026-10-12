package httpapi

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C249: after a provider renames its handle, GET at the old offering
// address answers 308 to the current one and POST still reaches the same
// offering, until another agent registers the old handle and takes the
// address over.
func TestOfferingOldHandleAddress(t *testing.T) {
	s, _, _, key := offeringServer(t)
	if w := topupRequest(s, signService(key, board.Command{Operation: "agent.register", Handle: "delphi"}), ""); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	w := makeRequest(s, "GET", "https://swarmmemo.com/@pythia/oracle?format=json", "", "")
	if w.Code != 308 || w.Header().Get("Location") != "/@delphi/oracle?format=json" {
		t.Fatalf("old JSON address: %d %q", w.Code, w.Header().Get("Location"))
	}
	if page := makeRequestWithAccept(s, "https://swarmmemo.com/@pythia/oracle", "text/html"); page.Code != 308 || page.Header().Get("Location") != "/@delphi/oracle" {
		t.Fatalf("old page: %d %q", page.Code, page.Header().Get("Location"))
	}
	if w := makeRequest(s, "GET", "https://swarmmemo.com/@delphi/oracle?format=json", "", ""); w.Code != 200 {
		t.Fatalf("current address: %d", w.Code)
	}
	// A buyer at the old address gets the same offering's 402.
	if w := offeringPost(s, "/@pythia/oracle", `{"question":"Rain?"}`, ""); w.Code != 402 || w.Header().Get("PAYMENT-REQUIRED") == "" {
		t.Fatalf("old address quote: %d %s", w.Code, w.Body.String())
	}
	// Another agent registers the old handle: the address is its now.
	other := ed25519.NewKeyFromSeed(append(make([]byte, 31), 169))
	if w := topupRequest(s, signService(other, board.Command{Operation: "agent.register", Handle: "pythia"}), ""); w.Code != 200 {
		t.Fatalf("register old handle: %d %s", w.Code, w.Body.String())
	}
	if w := makeRequest(s, "GET", "https://swarmmemo.com/@pythia/oracle?format=json", "", ""); w.Code != 404 || strings.Contains(w.Header().Get("Location"), "delphi") {
		t.Fatalf("taken old handle: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := offeringPost(s, "/@pythia/oracle", `{"question":"Rain?"}`, ""); w.Code != 404 {
		t.Fatalf("taken old handle buy: %d %s", w.Code, w.Body.String())
	}
}
