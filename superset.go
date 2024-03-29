package certdepot

import (
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
	"github.com/square/certstrap/depot"
	"github.com/square/certstrap/pkix"
)

func deleteIfExists(dpt depot.Depot, tags ...*depot.Tag) error {
	catcher := grip.NewBasicCatcher()
	for _, tag := range tags {
		if dpt.Check(tag) {
			catcher.Add(dpt.Delete(tag))
		}
	}
	return catcher.Resolve()
}

func depotSave(dpt Depot, name string, creds *Credentials) error {
	if err := deleteIfExists(dpt, CsrTag(name), PrivKeyTag(name), CrtTag(name)); err != nil {
		return errors.Wrap(err, "deleting existing credentials")
	}

	if err := dpt.Put(PrivKeyTag(name), creds.Key); err != nil {
		return errors.Wrap(err, "saving key")
	}

	if err := dpt.Put(CrtTag(name), creds.Cert); err != nil {
		return errors.Wrap(err, "saving certificate")
	}

	crt, err := pkix.NewCertificateFromPEM(creds.Cert)
	if err != nil {
		return errors.Wrap(err, "getting certificate from PEM bytes")
	}
	rawCrt, err := crt.GetRawCertificate()
	if err != nil {
		return errors.Wrap(err, "getting x509 certificate")
	}
	if err := putTTL(dpt, name, rawCrt.NotAfter); err != nil {
		return errors.Wrap(err, "putting expiration on credentials")
	}

	return nil
}

func depotGenerateDefault(dpt Depot, name string, do DepotOptions) (*Credentials, error) {
	return depotGenerate(dpt, name, do, CertificateOptions{
		CommonName: name,
		Host:       name,
	})
}

func depotGenerate(dpt Depot, name string, do DepotOptions, opts CertificateOptions) (*Credentials, error) {
	if opts.CA == "" {
		opts.CA = do.CA
	}
	if opts.Expires == 0 {
		opts.Expires = do.DefaultExpiration
	}

	_, key, err := opts.CertRequestInMemory()
	if err != nil {
		return nil, errors.Wrap(err, "making certificate request and key")
	}

	pemCACrt, err := dpt.Get(CrtTag(do.CA))
	if err != nil {
		return nil, errors.Wrap(err, "getting CA certificate")
	}

	pemKey, err := key.ExportPrivate()
	if err != nil {
		return nil, errors.Wrap(err, "exporting key")
	}

	crt, err := opts.SignInMemory(dpt)
	if err != nil {
		return nil, errors.Wrap(err, "signing certificate request")
	}

	pemCrt, err := crt.Export()
	if err != nil {
		return nil, errors.Wrap(err, "exporting certificate")
	}

	creds, err := NewCredentials(pemCACrt, pemCrt, pemKey)
	if err != nil {
		return nil, errors.Wrap(err, "creating credentials")
	}
	creds.ServerName = name

	return creds, nil
}

func depotFind(dpt depot.Depot, name string, do DepotOptions) (*Credentials, error) {
	caCrt, err := dpt.Get(CrtTag(do.CA))
	if err != nil {
		return nil, errors.Wrap(err, "getting CA certificate")
	}

	crt, err := dpt.Get(CrtTag(name))
	if err != nil {
		return nil, errors.Wrap(err, "getting certificate")
	}

	key, err := dpt.Get(PrivKeyTag(name))
	if err != nil {
		return nil, errors.Wrap(err, "getting key")
	}

	creds, err := NewCredentials(caCrt, crt, key)
	if err != nil {
		return nil, errors.Wrap(err, "creating credentials")
	}
	creds.ServerName = name

	return creds, nil
}
