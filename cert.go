package certdepot

import (
	"crypto/x509"
	"io/ioutil"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/square/certstrap/depot"
	"github.com/square/certstrap/pkix"
)

// CertificateOptions contains options to use for Init, CertRequest, and Sign.
type CertificateOptions struct {
	//
	// Options specific to Init and CertRequest.
	//
	// Passprhase to encrypt private-key PEM block.
	Passphrase string `bson:"passphrase,omitempty" json:"passphrase,omitempty" yaml:"passphrase,omitempty"`
	// Size (in bits) of RSA keypair to generate (defaults to 2048).
	KeyBits int `bson:"key_bits,omitempty" json:"key_bits,omitempty" yaml:"key_bits,omitempty"`
	// Sets the Organization (O) field of the certificate.
	Organization string `bson:"o,omitempty" json:"o,omitempty" yaml:"o,omitempty"`
	// Sets the Country (C) field of the certificate.
	Country string `bson:"c,omitempty" json:"c,omitempty" yaml:"c,omitempty"`
	// Sets the Locality (L) field of the certificate.
	Locality string `bson:"l,omitempty" json:"l,omitempty" yaml:"l,omitempty"`
	// Sets the Common Name (CN) field of the certificate.
	CommonName string `bson:"cn,omitempty" json:"cn,omitempty" yaml:"cn,omitempty"`
	// Sets the Organizational Unit (OU) field of the certificate.
	OrganizationalUnit string `bson:"ou,omitempty" json:"ou,omitempty" yaml:"ou,omitempty"`
	// Sets the State/Province (ST) field of the certificate.
	Province string `bson:"st,omitempty" json:"st,omitempty" yaml:"st,omitempty"`
	// IP addresses to add as subject alt name.
	IP []string `bson:"ip,omitempty" json:"ip,omitempty" yaml:"ip,omitempty"`
	// DNS entries to add as subject alt name.
	Domain []string `bson:"dns,omitempty" json:"dns,omitempty" yaml:"dns,omitempty"`
	// URI values to add as subject alt name.
	URI []string `bson:"uri,omitempty" json:"uri,omitempty" yaml:"uri,omitempty"`
	// Path to private key PEM file (if blank, will generate new keypair).
	Key string `bson:"key,omitempty" json:"key,omitempty" yaml:"key,omitempty"`

	//
	// Options specific to Init and Sign.
	//
	// How long until the certificate expires.
	Expires time.Duration `bson:"expires,omitempty" json:"expires,omitempty" yaml:"expires,omitempty"`

	//
	// Options specific to Sign.
	//
	// Host name of the certificate to be signed.
	Host string `bson:"host,omitempty" json:"host,omitempty" yaml:"host,omitempty"`
	// Name of CA to issue cert with.
	CA string `bson:"ca,omitempty" json:"ca,omitempty" yaml:"ca,omitempty"`
	// Passphrase to decrypt CA's private-key PEM block.
	CAPassphrase string `bson:"ca_passphrase,omitempty" json:"ca_passphrase,omitempty" yaml:"ca_passphrase,omitempty"`
	// Whether generated certificate should be an intermediate.
	Intermediate bool `bson:"intermediate,omitempty" json:"intermediate,omitempty" yaml:"intermediate,omitempty"`

	csr *pkix.CertificateSigningRequest
	key *pkix.Key
	crt *pkix.Certificate
}

// Init initializes a new CA.
func (opts *CertificateOptions) Init(wd Depot) error {
	if opts.CommonName == "" {
		return errors.New("must provide common name of CA")
	}
	formattedName := strings.Replace(opts.CommonName, " ", "_", -1)

	certExists, err := CheckCertificateWithError(wd, formattedName)
	if err != nil {
		return err
	}
	privKeyExists, err := CheckPrivateKeyWithError(wd, formattedName)
	if err != nil {
		return err
	}
	if certExists || privKeyExists {
		return errors.New("CA with specified name already exists")
	}

	key, err := opts.getOrCreatePrivateKey()
	if err != nil {
		return errors.WithStack(err)
	}

	expiresTime := time.Now().Add(opts.Expires)
	crt, err := pkix.CreateCertificateAuthority(
		key,
		opts.OrganizationalUnit,
		expiresTime,
		opts.Organization,
		opts.Country,
		opts.Province,
		opts.Locality,
		opts.CommonName,
		[]string{},
	)
	if err != nil {
		return errors.Wrap(err, "creating certificate authority")
	}

	if err = depot.PutCertificate(wd, formattedName, crt); err != nil {
		return errors.Wrap(err, "saving certificate authority")
	}

	if opts.Passphrase != "" {
		if err = depot.PutEncryptedPrivateKey(wd, formattedName, key, []byte(opts.Passphrase)); err != nil {
			return errors.Wrap(err, "saving encrypted private key")
		}
	} else {
		if err = depot.PutPrivateKey(wd, formattedName, key); err != nil {
			return errors.Wrap(err, "saving private key")
		}
	}

	// create an empty CRL, this is useful for Java apps which mandate a CRL
	crl, err := pkix.CreateCertificateRevocationList(key, crt, expiresTime)
	if err != nil {
		return errors.Wrap(err, "creating certificate revocation list")
	}
	if err = depot.PutCertificateRevocationList(wd, formattedName, crl); err != nil {
		return errors.Wrap(err, "saving certificate revocation list")
	}

	if md, ok := wd.(*mongoDepot); ok {
		rawCrt, err := crt.GetRawCertificate()
		if err != nil {
			return errors.Wrap(err, "getting raw cert")
		}
		if err = md.PutTTL(formattedName, rawCrt.NotAfter); err != nil {
			return errors.Wrap(err, "setting certificate TTL")
		}
	}
	return nil
}

// Reset clears the cached results of CertificateOptions so that the options
// can be changed after a certificate has already been requested or signed. For
// example, if the options have been modified, a new certificate request can be
// made with the new options by using Reset.
func (opts *CertificateOptions) Reset() {
	opts.csr = nil
	opts.key = nil
	opts.crt = nil
}

// CertRequest creates a new certificate signing request (CSR) and key and puts
// them in the depot.
func (opts *CertificateOptions) CertRequest(wd Depot) error {
	if _, _, err := opts.CertRequestInMemory(); err != nil {
		return errors.Wrap(err, "creating cert request and key")
	}

	return opts.PutCertRequestFromMemory(wd)
}

func (opts *CertificateOptions) certRequestedInMemory() bool {
	return opts.csr != nil && opts.key != nil
}

// CertRequestInMemory is the same as CertRequest but returns the resulting
// certificate signing request and private key without putting them in the
// depot. Use PutCertRequestFromMemory to put the certificate in the depot.
func (opts *CertificateOptions) CertRequestInMemory() (*pkix.CertificateSigningRequest, *pkix.Key, error) {
	if opts.certRequestedInMemory() {
		return opts.csr, opts.key, nil
	}

	ips, err := pkix.ParseAndValidateIPs(strings.Join(opts.IP, ","))
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parsing and validating IPs '%s'", opts.IP)
	}

	uris, err := pkix.ParseAndValidateURIs(strings.Join(opts.URI, ","))
	if err != nil {
		return nil, nil, errors.Wrapf(err, "parsing and validating URIs '%s'", opts.URI)
	}

	name, err := opts.getCertificateRequestName()
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	key, err := opts.getOrCreatePrivateKey()
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	csr, err := pkix.CreateCertificateSigningRequest(
		key,
		opts.OrganizationalUnit,
		ips,
		opts.Domain,
		uris,
		opts.Organization,
		opts.Country,
		opts.Province,
		opts.Locality,
		name,
	)
	if err != nil {
		return nil, nil, errors.Wrap(err, "creating certificate request")
	}

	opts.csr = csr
	opts.key = key

	return csr, key, nil
}

// PutCertRequestFromMemory stores the certificate request and key generated
// from the options in the depot.
func (opts *CertificateOptions) PutCertRequestFromMemory(wd Depot) error {
	if !opts.certRequestedInMemory() {
		return errors.New("must make cert request first before putting into depot")
	}

	formattedName, err := opts.getFormattedCertificateRequestName()
	if err != nil {
		return errors.Wrap(err, "getting formatted name")
	}

	csrExists, err := CheckCertificateSigningRequestWithError(wd, formattedName)
	if err != nil {
		return err
	}
	privKeyExists, err := CheckPrivateKeyWithError(wd, formattedName)
	if err != nil {
		return err
	}
	if csrExists || privKeyExists {
		return errors.New("certificate request or private key already exists")
	}

	if err = depot.PutCertificateSigningRequest(wd, formattedName, opts.csr); err != nil {
		return errors.Wrap(err, "saving certificate request")
	}

	if opts.Passphrase != "" {
		if err = depot.PutEncryptedPrivateKey(wd, formattedName, opts.key, []byte(opts.Passphrase)); err != nil {
			return errors.Wrap(err, "saving encrypted private key")
		}
	} else {
		if err = depot.PutPrivateKey(wd, formattedName, opts.key); err != nil {
			return errors.Wrap(err, "saving private key")
		}
	}

	return nil
}

// Sign signs a CSR with a given CA for a new certificate.
func (opts *CertificateOptions) Sign(wd Depot) error {
	_, err := opts.SignInMemory(wd)
	if err != nil {
		return errors.Wrap(err, "signing certificate request")
	}

	return opts.PutCertFromMemory(wd)
}

func (opts *CertificateOptions) signedInMemory() bool {
	return opts.crt != nil
}

// SignInMemory is the same as Sign but returns the resulting certificate
// without putting it in the depot. Use PutCertFromMemory to put the certificate
// in the depot.
func (opts *CertificateOptions) SignInMemory(wd Depot) (*pkix.Certificate, error) {
	if opts.signedInMemory() {
		return opts.crt, nil
	}
	if opts.Host == "" {
		return nil, errors.New("must provide name of host")
	}
	if opts.CA == "" {
		return nil, errors.New("must provide name of CA")
	}
	formattedReqName := strings.Replace(opts.Host, " ", "_", -1)
	formattedCAName := strings.Replace(opts.CA, " ", "_", -1)

	var csr *pkix.CertificateSigningRequest
	if opts.certRequestedInMemory() {
		csr = opts.csr
	} else {
		var err error
		csr, err = depot.GetCertificateSigningRequest(wd, formattedReqName)
		if err != nil {
			return nil, errors.Wrap(err, "getting host's certificate signing request")
		}
	}
	crt, err := depot.GetCertificate(wd, formattedCAName)
	if err != nil {
		return nil, errors.Wrap(err, "getting CA certificate")
	}

	// Validate that crt is allowed to sign certificates.
	rawCrt, err := crt.GetRawCertificate()
	if err != nil {
		return nil, errors.Wrap(err, "getting raw CA certificate")
	}
	// We punt on checking BasicConstraintsValid and checking MaxPathLen.
	// The goal is to prevent accidentally creating invalid certificates,
	// not protecting against malicious input.
	if !rawCrt.IsCA {
		return nil, errors.Errorf("'%s' is not allowed to sign certificates", opts.CA)
	}

	var key *pkix.Key
	if opts.CAPassphrase == "" {
		key, err = depot.GetPrivateKey(wd, formattedCAName)
		if err != nil {
			return nil, errors.Wrap(err, "getting unencrypted (assumed) CA key")
		}
	} else {
		key, err = depot.GetEncryptedPrivateKey(wd, formattedCAName, []byte(opts.CAPassphrase))
		if err != nil {
			return nil, errors.Wrap(err, "getting encrypted CA key")
		}
	}

	expiresTime := time.Now().Add(opts.Expires)
	var crtOut *pkix.Certificate
	if opts.Intermediate {
		crtOut, err = pkix.CreateIntermediateCertificateAuthority(crt, key, csr, expiresTime)
	} else {
		crtOut, err = pkix.CreateCertificateHost(crt, key, csr, expiresTime)
	}
	if err != nil {
		return nil, errors.Wrap(err, "creating certificate")
	}

	opts.crt = crtOut

	return crtOut, nil
}

// PutCertFromMemory stores the certificate generated from the options in the
// depot, along with the expiration TTL on the certificate.
func (opts *CertificateOptions) PutCertFromMemory(wd Depot) error {
	if !opts.signedInMemory() {
		return errors.New("must sign cert first before putting into depot")
	}
	formattedReqName := strings.Replace(opts.Host, " ", "_", -1)

	exists, err := CheckCertificateWithError(wd, formattedReqName)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("certificate already exists")
	}

	if err := depot.PutCertificate(wd, formattedReqName, opts.crt); err != nil {
		return errors.Wrap(err, "saving certificate")
	}

	if md, ok := wd.(*mongoDepot); ok {
		rawCrt, err := opts.crt.GetRawCertificate()
		if err != nil {
			return errors.Wrap(err, "getting raw certificate")
		}
		if err = md.PutTTL(formattedReqName, rawCrt.NotAfter); err != nil {
			return errors.Wrap(err, "saving certificate TTL")
		}
	}

	return nil
}

func getFormattedCertificateRequestName(name string) (string, error) {
	filenameAcceptable, err := regexp.Compile("[^a-zA-Z0-9._-]")
	if err != nil {
		return "", errors.Wrap(err, "compiling regex for acceptable certificate request names")
	}
	return string(filenameAcceptable.ReplaceAll([]byte(name), []byte("_"))), nil
}

func (opts CertificateOptions) getFormattedCertificateRequestName() (string, error) {
	name, err := opts.getCertificateRequestName()
	if err != nil {
		return "", errors.Wrap(err, "getting name for certificate request")
	}
	return getFormattedCertificateRequestName(name)
}

func (opts CertificateOptions) getCertificateRequestName() (string, error) {
	switch {
	case opts.CommonName != "":
		return opts.CommonName, nil
	case len(opts.Domain) != 0:
		return opts.Domain[0], nil
	default:
		return "", errors.New("must provide a common name or domain")
	}
}

func (opts CertificateOptions) getOrCreatePrivateKey() (*pkix.Key, error) {
	var key *pkix.Key
	if opts.Key != "" {
		keyBytes, err := ioutil.ReadFile(opts.Key)
		if err != nil {
			return nil, errors.Wrapf(err, "reading key '%s'", opts.Key)
		}
		key, err = pkix.NewKeyFromPrivateKeyPEM(keyBytes)
		if err != nil {
			return nil, errors.Wrap(err, "getting key from PEM")
		}
	} else {
		if opts.KeyBits == 0 {
			opts.KeyBits = 2048
		}
		var err error
		key, err = pkix.CreateRSAKey(opts.KeyBits)
		if err != nil {
			return nil, errors.Wrap(err, "creating RSA key")
		}
	}
	return key, nil
}

func getNameAndKey(tag *depot.Tag) (string, string, error) {
	if name := depot.GetNameFromCrtTag(tag); name != "" {
		return strings.Replace(name, " ", "_", -1), userCertKey, nil
	}
	if name := depot.GetNameFromPrivKeyTag(tag); name != "" {
		return strings.Replace(name, " ", "_", -1), userPrivateKeyKey, nil
	}
	if name := depot.GetNameFromCsrTag(tag); name != "" {
		formattedName, err := getFormattedCertificateRequestName(name)
		return formattedName, userCertReqKey, err
	}
	if name := depot.GetNameFromCrlTag(tag); name != "" {
		return strings.Replace(name, " ", "_", -1), userCertRevocListKey, nil
	}
	return "", "", nil
}

// CreateCertificate is a convenience function for creating a certificate
// request and signing it.
func (opts *CertificateOptions) CreateCertificate(wd Depot) error {
	if err := opts.CertRequest(wd); err != nil {
		return errors.Wrap(err, "creating the certificate request")
	}
	if err := opts.Sign(wd); err != nil {
		return errors.Wrap(err, "signing the certificate request")
	}

	return nil
}

// CreateCertificateOnExpiration checks if a certificate does not exist or if
// it expires within the duration `after` and creates a new certificate if
// either condition is met. True is returned if a certificate is created,
// false otherwise. If the certificate is a CA, the behavior is undefined.
func (opts *CertificateOptions) CreateCertificateOnExpiration(wd Depot, after time.Duration) (bool, error) {
	var (
		exists  bool
		created bool
		err     error
	)
	dne := true

	if exists, err = CheckCertificateWithError(wd, opts.CommonName); err != nil {
		return created, err
	} else if exists {
		dne, err = DeleteOnExpiration(wd, opts.CommonName, after)
		if err != nil {
			return created, errors.Wrap(err, "deleting expiring certificate")
		}
	}

	if dne {
		err = opts.CreateCertificate(wd)
		created = true
	}

	return created, errors.Wrap(err, "creating certificate")
}

// ValidityBounds returns the date range for which the certificate is valid.
func ValidityBounds(wd Depot, name string) (time.Time, time.Time, error) {
	rawCert, err := getRawCertificate(wd, name)
	if err != nil {
		return time.Time{}, time.Time{}, errors.Wrap(err, "getting raw certificate")
	}

	return rawCert.NotBefore, rawCert.NotAfter, nil
}

// DeleteOnExpiration deletes the given certificate from the depot if it has an
// expiration date within the duration `after`. True is returned if the
// certificate is deleted, false otherwise.
func DeleteOnExpiration(wd Depot, name string, after time.Duration) (bool, error) {
	var deleted bool

	if exists, err := CheckCertificateWithError(wd, name); err != nil {
		return deleted, err
	} else if !exists {
		return deleted, nil
	}

	rawCert, err := getRawCertificate(wd, name)
	if err != nil {
		return deleted, errors.Wrap(err, "getting raw certificate")
	}

	if rawCert.NotAfter.Before(time.Now().Add(after)) {
		err = depot.DeleteCertificate(wd, name)
		if err != nil {
			return deleted, errors.Wrap(err, "deleting expiring certificate")
		}

		if !rawCert.IsCA {
			err = depot.DeleteCertificateSigningRequest(wd, name)
			if err != nil {
				return deleted, errors.Wrap(err, "deleting expiring certificate signing request")
			}
		}

		err = wd.Delete(depot.PrivKeyTag(name))
		if err != nil {
			return deleted, errors.Wrap(err, "deleting expiring certificate key")
		}

		deleted = true
	}

	return deleted, nil
}

func getRawCertificate(d Depot, name string) (*x509.Certificate, error) {
	cert, err := depot.GetCertificate(d, name)
	if err != nil {
		return nil, errors.Wrap(err, "getting certificate from the depot")
	}

	rawCert, err := cert.GetRawCertificate()
	return rawCert, errors.WithStack(err)
}
