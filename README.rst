======================================
``certdepot`` -- SSL Certificate Store
======================================

Overview
--------

Tools for creating and storing SSL certificates.


Motivation
----------

Certdepot is based off `certstrap <https://github.com/square/certstrap>`_ by
Square. This project heavily depends certstrap by expanding its capabilities.

Features
--------

Certificate Creation and Signing
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

SSL certificates and certificate authorities (CAs) can easily be created and
signed using Certdepot. 


MongoDB Backed Depot
~~~~~~~~~~~~~~~~~~~~

Certdepot implements a certstrap 
`depot <https://godoc.org/github.com/square/certstrap/depot#Depot>`_ backed by
MongoDB. This facilitates the storing and fetching of SSL certificates to and
from a Mongo database. There are various functions for maintaining the depot,
such as checking for expiration and rotating certs.


Bootstrap
~~~~~~~~~

Bootsrapping a depot facilitates creating a certificate depot with both a CA
and service certificate. ``BootstrapDepot`` currently supports bootstrapping
``FileDepots`` and ``MongoDepots``.


Code Examples
-------------

Create a depot, initialize a CA in the depot, and create and sign service cert
with that CA in the depot: ::
	mongoOpts := certdepot.MongoDBOptions{} // populate options
	d, err := certdepot.NewMongoDBCertDepot(ctx, mongoOpts)
	// handle err

	certOpts := certdepot.CertificateOptions{
		Organization:       "mongodb",
		Country:            "USA",
		Locality:           "NYC",
		OrganizationalUnit: "evergreen",
		Province:           "Manhattan",
		Expires:            24 * time.Hour,

		IP:           []string{"0.0.0.0"},
		Domain:       []string{"evergreen"},
		URI:          []string{"evergreen.mongodb.com"},
		Host:         "evergreen",
		CA:           "ca",
		CAPassphrase: "passphrase",
		Intermediate: true,
	}

	// initialize CA named `ca` and stores it in the depot
	certOpts.Init(d)
	// creates a new certificate named `evergreen`, signs it with `ca`, and
	// stores it in the depot
	certOpts.CreateCertificate(d)

The following does the same as above, but now using the bootstrap 
functionality: ::
	bootstrapConf := certdepot.BootstrapDepotConfig{
                MongoDepot:  mongoOpts,
		CAOpts:      certOpts,
		ServiceOpts: certOpts,
	}
	d, err := BootstrapDepot(ctx, bootstrapConf)


Tests
-----

To run tests simply run `make test` from the root directory of the project. The
tests require a local mongod to be running.

Documentation
-------------

See the 
`certdepot godoc <https://godoc.org/github.com/evergreen-ci/certdepot>`_ for
complete documentation of certdepot.

See the `certstrap godoc <https://godoc.org/github.com/square/certstrap>`_ for
complete documentation of certstrap.
