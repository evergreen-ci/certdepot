package certdepot

import "github.com/square/certstrap/depot"

// Depot is a wrapper around certrstap's depot.Depot interface so users only
// need to vendor certdepot.
type Depot depot.Depot

// NewFileDepot is a wrapper to create a FileDepot wrapped with
// certdepot.Depot.
func NewFileDepot(dir string) (Depot, error) {
	return depot.NewFileDepot(dir)
}
