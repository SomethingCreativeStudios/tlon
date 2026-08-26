// Package auth defines the authorization seam used by both the standalone
// server and embedders.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// Operation identifies an API action without coupling callers to a router.
type Operation string

const (
	OperationCreate  Operation = "record.create"
	OperationReplace Operation = "record.replace"
	OperationPatch   Operation = "record.patch"
	OperationDelete  Operation = "record.delete"
)

// Action is provided to an Authorizer for every mutation. Request is present
// for HTTP calls and may be nil for an embedded application.
type Action struct {
	Operation Operation
	CatalogID string
	RecordID  string
	Request   *http.Request
}

// Authorizer decides whether a mutation may proceed. Reads are intentionally
// public and never pass through this interface.
type Authorizer interface {
	Authorize(context.Context, Action) error
}

// ErrForbidden is returned when a write is not authorized.
var ErrForbidden = errors.New("mutation authorization denied")

// DenyMutations is the safe standalone default.
type DenyMutations struct{}

func (DenyMutations) Authorize(context.Context, Action) error { return ErrForbidden }

// External authorizes all mutations because a trusted deployment proxy is
// responsible for authenticating users and enforcing method/catalog policy.
type External struct{}

func (External) Authorize(context.Context, Action) error { return nil }
