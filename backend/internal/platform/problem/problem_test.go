package problem_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/problem"
)

// errorCodeSchema is contracts/ read from the module it generates into.
//
// The path leaves the Go module, which is unusual and deliberate: this test
// asserts that the rendering table covers exactly what the contract declares,
// and the only way to assert that is against the contract itself. Testing
// against the generated Go constants instead would pass while the schema and
// the table drifted, because both would have been regenerated from the same
// stale assumption.
const errorCodeSchema = "../../../../contracts/evidence/error_code.json"

func declaredCodes(t *testing.T) []generated.ErrorCode {
	t.Helper()

	raw, err := os.ReadFile(errorCodeSchema)
	if err != nil {
		t.Fatalf("read %s: %v", errorCodeSchema, err)
	}
	var schema struct {
		Enum []generated.ErrorCode `json:"enum"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse %s: %v", errorCodeSchema, err)
	}
	if len(schema.Enum) == 0 {
		t.Fatalf("%s declares no codes", errorCodeSchema)
	}
	return schema.Enum
}

// TestEveryCodeRenders is the test this package exists for. A code added to
// contracts/ and not given a rendering would otherwise be discovered by a
// panic, in whichever role first tried to reject a request with it.
func TestEveryCodeRenders(t *testing.T) {
	t.Parallel()

	for _, code := range declaredCodes(t) {
		t.Run(string(code), func(t *testing.T) {
			t.Parallel()

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("no rendering: %v", r)
				}
			}()
			p := problem.New(code, "")

			if p.Code != code {
				t.Errorf("Code = %q, want %q", p.Code, code)
			}
			if p.Title == "" {
				t.Error("Title is empty")
			}
			if p.Type != "urn:agentic-payments:error:"+string(code) {
				t.Errorf("Type = %q, want the urn form of %q", p.Type, code)
			}
			// RFC 9110 reserves 1xx-3xx for outcomes that are not failures, so
			// a Problem carrying one would describe a rejection with a status
			// line saying nothing was rejected.
			if p.Status < 400 || p.Status > 599 {
				t.Errorf("Status = %d, want a 4xx or 5xx", p.Status)
			}
		})
	}
}

// TestStatusClassMatchesFault checks the rule ADR 0001 states: 4xx is the
// caller's fault, 5xx is the verifier's. Only one code in the taxonomy is the
// verifier's own, so the assertion is cheap and the drift it catches is real —
// classifying a caller error as 5xx tells a client to retry something that
// will never succeed.
func TestStatusClassMatchesFault(t *testing.T) {
	t.Parallel()

	for _, code := range declaredCodes(t) {
		p := problem.New(code, "")
		serverFault := code == generated.ErrorCodeVerifierUnavailable
		got5xx := p.Status >= 500

		if got5xx != serverFault {
			t.Errorf("%s: status %d, want %s",
				code, p.Status, map[bool]string{true: "5xx", false: "4xx"}[serverFault])
		}
	}
}

// TestWrite covers the wire form: the media type, the status line agreeing
// with the body, and the code surviving the round trip.
func TestWrite(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	p := problem.New(generated.ErrorCodeConstraintViolated, "price.max is 20000, checkout is 21000")
	if err := p.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := rec.Header().Get("Content-Type"); got != problem.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, problem.ContentType)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	var back problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if back.Code != generated.ErrorCodeConstraintViolated {
		t.Errorf("code = %q, want %q", back.Code, generated.ErrorCodeConstraintViolated)
	}
	if back.Status != rec.Code {
		t.Errorf("body status %d disagrees with the status line %d", back.Status, rec.Code)
	}
	if back.Detail == "" {
		t.Error("detail was dropped")
	}
}

// TestDetailIsOptional pins that the operator-facing text may be absent and
// does not then appear as an empty member — nothing may branch on detail, and
// an empty string invites exactly that.
func TestDetailIsOptional(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(problem.New(generated.ErrorCodeKeyUnknown, ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var members map[string]any
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := members["detail"]; present {
		t.Errorf("detail present when empty: %s", body)
	}
	for _, required := range []string{"type", "title", "status", "code"} {
		if _, present := members[required]; !present {
			t.Errorf("%s missing: %s", required, body)
		}
	}
}

// TestSameCodeOnBothSurfaces is the property ADR 0001 was written to protect:
// the value a Problem Details response carries and the value a Receipt carries
// are the same value, not two lists that happen to agree today.
func TestSameCodeOnBothSurfaces(t *testing.T) {
	t.Parallel()

	code := generated.ErrorCodeCheckoutHashMismatch

	receipt := generated.Receipt{
		Issuer:      "https://merchant.example",
		MandateType: generated.ReceiptMandateTypeCheckout,
		Reference:   "0_Af-2B-EhLWX5ydh_w2xzwmO6iM66B_2QCEanI4fUY",
		Result:      generated.ReceiptResultError,
		Error:       &code,
	}
	p := problem.New(code, "")

	// This comparison is itself the compile-time half of the guarantee. Go
	// will not compare values of two different named types, so it only builds
	// because the receipt's error field and the problem's code are the same
	// generated type — which is what stops either surface drifting onto a
	// list of its own. The runtime assertion below is the cheaper half.
	if *receipt.Error != p.Code {
		t.Fatalf("receipt carries %q, problem carries %q", *receipt.Error, p.Code)
	}
}
