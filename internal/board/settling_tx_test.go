package board

import (
	"database/sql"
	"testing"
)

// One transaction settles one item, whatever recorded it first: a work reward, a bond or
// its refund, an offering payment or its refund (round 6, newbotlabor).
func TestSettlingTxUsedCoversEveryTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE work_usdc (tx_hash TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE proof_bonds (tx_hash TEXT NOT NULL DEFAULT '', refund_tx_hash TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE offering_calls (tx_hash TEXT NOT NULL DEFAULT '', refund_tx TEXT NOT NULL DEFAULT '')",
		"INSERT INTO work_usdc(tx_hash) VALUES('0xa1'),('')",
		"INSERT INTO proof_bonds(tx_hash, refund_tx_hash) VALUES('0xb1','0xb2'),('','')",
		"INSERT INTO offering_calls(tx_hash, refund_tx) VALUES('0xc1','0xc2'),('','')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for hash, want := range map[string]bool{"0xa1": true, "0xb1": true, "0xb2": true, "0xc1": true, "0xc2": true, "0xd4": false, "": false} {
		got, err := settlingTxUsed(testContext, tx, hash)
		if err != nil || got != want {
			t.Errorf("settlingTxUsed(%q) = %v, %v; want %v", hash, got, err, want)
		}
	}
}
