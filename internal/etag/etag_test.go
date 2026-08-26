package etag

import "testing"

func TestRoundTrip(t *testing.T) {
	tag := FromVersion(42)
	version, err := Parse(tag)
	if err != nil || version != 42 {
		t.Fatalf("Parse(%q) = %d, %v", tag, version, err)
	}
}
func TestRejectWeakAndForeign(t *testing.T) {
	for _, tag := range []string{`W/"tlon-1"`, `"other"`, `*`, `"tlon-0"`} {
		if _, err := Parse(tag); err == nil {
			t.Errorf("accepted %q", tag)
		}
	}
}
