// Package ap2 translates between the Google Agent Payments Protocol wire
// format and the canonical model in internal/core.
//
// Role-specific verification lives here: Merchant, Credential Provider,
// Network and Merchant Payment Processor each verify different things about
// the same mandate.
package ap2
