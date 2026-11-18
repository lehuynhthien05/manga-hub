package notifyudp

import (
	"os"
	"testing"

	"mangahub/internal/testutil"
)

// TestMain fails the package if any goroutine started by its tests outlives them.
func TestMain(m *testing.M) { os.Exit(testutil.LeakCheckMain(m)) }
