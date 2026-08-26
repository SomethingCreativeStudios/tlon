package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SomethingCreativeStudios/tlon/store"
)

var ErrInvalid = errors.New("invalid cursor")

type payload struct {
	Version   int                   `json:"v"`
	CatalogID string                `json:"catalog"`
	Scope     string                `json:"scope"`
	Sort      []store.SortField     `json:"sort"`
	Direction store.CursorDirection `json:"direction"`
	Values    []any                 `json:"values"`
}

type Codec struct{ secret []byte }

func New(secret string) (*Codec, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("cursor secret must be at least 32 bytes")
	}
	return &Codec{secret: []byte(secret)}, nil
}

func Scope(search store.Search) (string, error) {
	copy := search
	copy.Position = nil
	copy.Limit = 0
	// Facet selection changes only response enrichment, not the filtered or
	// sorted record sequence. Allow clients to suppress repeated facet work
	// while traversing an existing cursor.
	copy.Facets = nil
	b, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (c *Codec) Encode(catalogID, scope string, sort []store.SortField, direction store.CursorDirection, values []any) (string, error) {
	b, err := json.Marshal(payload{Version: 1, CatalogID: catalogID, Scope: scope, Sort: sort, Direction: direction, Values: values})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *Codec) Decode(token, catalogID, scope string, sort []store.SortField) (*store.CursorPosition, error) {
	parts := split2(token, '.')
	if parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalid
	}
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, ErrInvalid
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil || p.Version != 1 || p.CatalogID != catalogID || p.Scope != scope || !sameSort(p.Sort, sort) || (p.Direction != store.CursorNext && p.Direction != store.CursorPrev) {
		return nil, ErrInvalid
	}
	return &store.CursorPosition{Direction: p.Direction, Values: p.Values}, nil
}

func split2(s string, sep byte) [2]string {
	for i := range s {
		if s[i] == sep {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{}
}

func sameSort(a, b []store.SortField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
