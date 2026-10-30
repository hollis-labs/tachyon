package approvals

import (
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvalsapi"
)

// The contract package restates the prefix so it need not import this package.
func TestContractPrefixMatchesTheInbox(t *testing.T) {
	if approvalsapi.Prefix != Prefix {
		t.Fatalf("%s != %s", approvalsapi.Prefix, Prefix)
	}
}
