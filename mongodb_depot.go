package certdepot

import (
	"context"

	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
	"github.com/square/certstrap/depot"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoDepot struct {
	ctx            context.Context
	client         *mongo.Client
	databaseName   string
	collectionName string
	opts           DepotOptions
}

// NewMongoDBCertDepot returns a new cert depot backed by MongoDB using the
// mongo driver.
func NewMongoDBCertDepot(ctx context.Context, opts *MongoDBOptions) (Depot, error) {
	if err := opts.validate(); err != nil {
		return nil, errors.Wrap(err, "invalid options")
	}

	client, err := mongo.Connect(options.Client().ApplyURI(opts.MongoDBURI).SetConnectTimeout(opts.MongoDBDialTimeout))
	if err != nil {
		return nil, errors.Wrap(err, "connecting to database")
	}

	return &mongoDepot{
		ctx:            ctx,
		client:         client,
		databaseName:   opts.DatabaseName,
		collectionName: opts.CollectionName,
		opts:           opts.DepotOptions,
	}, nil
}

// NewMongoDBCertDepotWithClient returns a new cert depot backed by MongoDB
// using the provided mongo driver client.
func NewMongoDBCertDepotWithClient(ctx context.Context, client *mongo.Client, opts *MongoDBOptions) (Depot, error) {
	if client == nil {
		return nil, errors.New("must specify a non-nil client")
	}

	if err := opts.validate(); err != nil {
		return nil, errors.Wrap(err, "invalid options")
	}

	return &mongoDepot{
		ctx:            ctx,
		client:         client,
		databaseName:   opts.DatabaseName,
		collectionName: opts.CollectionName,
		opts:           opts.DepotOptions,
	}, nil
}

// Put inserts the data into the document specified by the tag.
func (m *mongoDepot) Put(tag *depot.Tag, data []byte) error {
	if data == nil {
		return errors.New("data is nil")
	}

	name, key, err := getNameAndKey(tag)
	if err != nil {
		return errors.Wrapf(err, "formatting name '%s'", name)
	}

	update := bson.M{"$set": bson.M{key: string(data)}}

	res, err := m.client.Database(m.databaseName).Collection(m.collectionName).UpdateOne(m.ctx,
		bson.D{{Key: userIDKey, Value: name}},
		update,
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return errors.Wrap(err, "adding data to the database")
	}
	grip.Debug(message.Fields{
		"db":       m.databaseName,
		"coll":     m.collectionName,
		"id":       name,
		"matched":  res.MatchedCount,
		"modified": res.ModifiedCount,
		"op":       "put",
	})

	return nil
}

// Check returns whether the user and data specified by the tag exists.
func (m *mongoDepot) Check(tag *depot.Tag) bool {
	name, key, err := getNameAndKey(tag)
	if err != nil {
		return false
	}

	u := &User{}

	err = m.client.Database(m.databaseName).Collection(m.collectionName).FindOne(m.ctx, bson.D{{Key: userIDKey, Value: name}}).Decode(u)
	grip.WarningWhen(errNotNoDocuments(err), message.WrapError(err, message.Fields{
		"db":   m.databaseName,
		"coll": m.collectionName,
		"id":   name,
		"op":   "check",
	}))

	switch key {
	case userCertKey:
		return u.Cert != ""
	case userPrivateKeyKey:
		return u.PrivateKey != ""
	case userCertReqKey:
		return u.CertReq != ""
	case userCertRevocListKey:
		return u.CertRevocList != ""
	default:
		return false
	}
}

// CheckWithError returns whether the user and data specified by the tag exists
// as well as an error in the case of an internal error.
func (m *mongoDepot) CheckWithError(tag *depot.Tag) (bool, error) {
	name, key, err := getNameAndKey(tag)
	if err != nil {
		return false, errors.Wrap(err, "getting name and key")
	}

	u := &User{}

	err = m.client.Database(m.databaseName).Collection(m.collectionName).FindOne(m.ctx, bson.D{{Key: userIDKey, Value: name}}).Decode(u)
	if errNotNoDocuments(err) {
		return false, errors.Wrap(err, "checking depot tag")
	}

	switch key {
	case userCertKey:
		return u.Cert != "", nil
	case userPrivateKeyKey:
		return u.PrivateKey != "", nil
	case userCertReqKey:
		return u.CertReq != "", nil
	case userCertRevocListKey:
		return u.CertRevocList != "", nil
	default:
		return false, nil
	}
}

// Get reads the data for the user specified by tag. Returns an error if the
// user does not exist or if the data is empty.
func (m *mongoDepot) Get(tag *depot.Tag) ([]byte, error) {
	name, key, err := getNameAndKey(tag)
	if err != nil {
		return nil, errors.Wrapf(err, "formatting name '%s'", name)
	}

	u := &User{}
	if err = m.client.Database(m.databaseName).Collection(m.collectionName).FindOne(m.ctx, bson.D{{Key: userIDKey, Value: name}}).Decode(u); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, errors.Wrapf(err, "name '%s' not found", name)
		}
		return nil, errors.Wrapf(err, "looking up name '%s' in the database", name)
	}

	var data []byte
	switch key {
	case userCertKey:
		data = []byte(u.Cert)
	case userPrivateKeyKey:
		data = []byte(u.PrivateKey)
	case userCertReqKey:
		data = []byte(u.CertReq)
	case userCertRevocListKey:
		data = []byte(u.CertRevocList)
	}

	if len(data) == 0 {
		return nil, errors.New("no data available")
	}
	return data, nil
}

// Delete removes the data from a user specified by the tag.
func (m *mongoDepot) Delete(tag *depot.Tag) error {
	name, key, err := getNameAndKey(tag)
	if err != nil {
		return errors.Wrapf(err, "formatting name '%s'", name)
	}

	if _, err = m.client.Database(m.databaseName).Collection(m.collectionName).UpdateOne(m.ctx,
		bson.D{{Key: userIDKey, Value: name}},
		bson.M{"$unset": bson.M{key: ""}}); errNotNoDocuments(err) {
		return errors.Wrapf(err, "deleting '%s.%s' from the database", name, key)
	}

	return nil
}
func (m *mongoDepot) Save(name string, creds *Credentials) error { return depotSave(m, name, creds) }
func (m *mongoDepot) Find(name string) (*Credentials, error)     { return depotFind(m, name, m.opts) }
func (m *mongoDepot) Generate(name string) (*Credentials, error) {
	return depotGenerateDefault(m, name, m.opts)
}

func (m *mongoDepot) GenerateWithOptions(opts CertificateOptions) (*Credentials, error) {
	return depotGenerate(m, opts.CommonName, m.opts, opts)
}

func errNotNoDocuments(err error) bool {
	return err != nil && err != mongo.ErrNoDocuments
}
