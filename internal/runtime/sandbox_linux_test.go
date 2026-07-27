//go:build linux

package runtime

import (
	"testing"
	"time"
)

func TestBwrapProbeCacheExpires(t *testing.T) {
	bwrapCache.Lock()
	bwrapCache.path = "cached"
	bwrapCache.at = time.Now().Add(-bwrapCacheTTL - time.Second)
	bwrapCache.Unlock()

	_ = lookPathBwrap()
	bwrapCache.RLock()
	at := bwrapCache.at
	bwrapCache.RUnlock()
	if time.Since(at) >= bwrapCacheTTL {
		t.Fatalf("probe timestamp was not refreshed: %v", at)
	}
}
