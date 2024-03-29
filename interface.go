package certdepot

import (
	"time"

	"github.com/square/certstrap/depot"
)

// Depot is a superset wrapper around certrstap's depot.Depot interface.
type Depot interface {
	depot.Depot
	CheckWithError(tag *depot.Tag) (bool, error)
	Save(string, *Credentials) error
	Find(string) (*Credentials, error)
	Generate(string) (*Credentials, error)
	GenerateWithOptions(CertificateOptions) (*Credentials, error)
}

// DepotOptions capture default options used during certificate
// generation and creation used by depots.
type DepotOptions struct {
	CA                string        `bson:"ca" json:"ca" yaml:"ca"`
	DefaultExpiration time.Duration `bson:"default_expiration" json:"default_expiration" yaml:"default_expiration"`
}
