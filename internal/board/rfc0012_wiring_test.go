package board

import (
	"testing"

	"swarmmemo/internal/trust"
)

// The integration of RFC0012's builders: one ledger for posting and
// services, one parameter store that validates every namespace, and
// classifier v1 under TRUST=allocation.
func TestRFC0012Wiring(t *testing.T) {
	s := openTest(t, Config{Features: Features{Services: []string{"memory", "echo"}}})
	if s.serviceMeter() != s.allowanceLedger() {
		t.Fatal("the services meter through a ledger of their own")
	}
	for _, tc := range []struct {
		namespace, good, bad string
		want                 int64
	}{
		{"services", `{"schema":1,"prices":{}}`, `{"schema":1,"prices":{"nope":{}}}`, 1},
		{"trust", string(trust.DefaultParams().Body()), `{"schema":1}`, trust.DefaultVersion + 1},
	} {
		if _, err := s.SetAllowanceParams(testContext, tc.namespace, []byte(tc.bad), "test", 0); err == nil {
			t.Fatalf("%s: an invalid body was stored", tc.namespace)
		}
		if v, err := s.SetAllowanceParams(testContext, tc.namespace, []byte(tc.good), "test", 0); err != nil || v != tc.want {
			t.Fatalf("%s: version %d, %v", tc.namespace, v, err)
		}
	}
	pv, err := s.AllowanceParams(testContext, "trust", trust.DefaultVersion)
	if err != nil || !pv.CompiledIn {
		t.Fatalf("trust version %d: %+v %v", trust.DefaultVersion, pv, err)
	}
	if _, err := trust.ParseParams(pv.Version, pv.Body); err != nil {
		t.Fatal(err)
	}
	// One classifier, trust on or off: the standing (standing.go).
	for _, st := range []*Store{s, openTest(t, Config{Features: Features{Trust: TrustOn}})} {
		if _, ok := st.classifier().(standingClassifier); !ok {
			t.Fatalf("classifier %T is not the standing", st.classifier())
		}
	}
}
