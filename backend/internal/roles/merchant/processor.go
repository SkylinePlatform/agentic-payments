package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// Processor is the Merchant Payment Processor, as the merchant sees it.
//
// An interface because AP2 has the *merchant* initiate payment, so this call is
// the merchant's — and a merchant that constructed its processor inline could
// not be tested without one listening, nor pointed at a different one. The same
// reasoning that makes the verification rules injectable applies here: the
// party is a dependency, not an implementation detail.
type Processor interface {
	// InitiatePayment presents the payment side of a purchase and returns the
	// processor's signed answer. A refusal is not an error — the receipt is the
	// answer either way, and settled is what says which it was.
	InitiatePayment(
		ctx context.Context,
		paymentMandate string,
		credential generated.PaymentCredential,
	) (receipt string, settled bool, err error)
}

// HTTPProcessor talks to a Merchant Payment Processor over HTTP.
type HTTPProcessor struct {
	// Base is the processor's root, e.g. "http://localhost:8083".
	Base string
	// Client is the HTTP client to use. Zero means http.DefaultClient.
	Client *http.Client
}

var _ Processor = (*HTTPProcessor)(nil)

// InitiatePayment sends the Payment Mandate and the credential to the processor.
//
// A 4xx is a refusal rather than a transport failure, and it carries a receipt.
// Returning an error on the status would discard the processor's signed account
// of why it would not move the money — which is the one thing a merchant will
// want when the customer asks.
func (p *HTTPProcessor) InitiatePayment(
	ctx context.Context,
	paymentMandate string,
	credential generated.PaymentCredential,
) (string, bool, error) {
	body, err := json.Marshal(map[string]any{
		"mandate":    paymentMandate,
		"credential": credential,
	})
	if err != nil {
		return "", false, fmt.Errorf("encoding the settlement: %w", err)
	}

	url := strings.TrimSuffix(p.Base, "/") + "/payment"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("building the settlement request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", roles.Fingerprint(url+" "+string(body)))

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("calling the processor at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Receipt string `json:"receipt"`
		Settled bool   `json:"settled"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding the processor's answer: %w", err)
	}

	// 5xx is the processor failing rather than refusing, and there is no signed
	// answer in it to pass on.
	if resp.StatusCode >= 500 {
		return "", false, fmt.Errorf("the processor answered %s", resp.Status)
	}
	return out.Receipt, out.Settled, nil
}
