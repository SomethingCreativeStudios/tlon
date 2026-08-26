package etag

import (
	"fmt"
	"strconv"
	"strings"
)

func FromVersion(version int64) string { return fmt.Sprintf("\"tlon-%d\"", version) }

func Parse(value string) (int64, error) {
	if strings.HasPrefix(value, "W/") {
		return 0, fmt.Errorf("weak entity tags are not accepted")
	}
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "\"tlon-") || !strings.HasSuffix(v, "\"") {
		return 0, fmt.Errorf("invalid Tlon entity tag")
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(v, "\"tlon-"), "\""), 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid Tlon entity tag")
	}
	return n, nil
}
