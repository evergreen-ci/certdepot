package certdepot

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/square/certstrap/depot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func getTagPath(tag *depot.Tag) string {
	if name := GetNameFromCrtTag(tag); name != "" {
		return name + ".crt"
	}
	if name := GetNameFromPrivKeyTag(tag); name != "" {
		return name + ".key"
	}
	if name := GetNameFromCsrTag(tag); name != "" {
		return name + ".csr"
	}
	if name := GetNameFromCrlTag(tag); name != "" {
		return name + ".crl"
	}
	return ""
}

func TestDepot(t *testing.T) {
	var tempDir string
	var data []byte

	const databaseName = "certDepot"
	const collectionName = "certs"

	ctx := context.TODO()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	require.NoError(t, err)

	defer func() {
		dropContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		err = client.Database(databaseName).Collection(collectionName).Drop(dropContext)
		if err != nil {
			assert.Equal(t, "ns not found", err.Error())
		}
	}()

	type testCase struct {
		name string
		test func(t *testing.T, d Depot)
	}

	for _, impl := range []struct {
		name      string
		setup     func() Depot
		bootstrap func(t *testing.T) Depot
		check     func(*testing.T, *depot.Tag, []byte)
		cleanup   func()
		tests     []testCase
	}{
		{
			name: "File",
			setup: func() Depot {
				tempDir, err = ioutil.TempDir(".", "file_depot")
				require.NoError(t, err)
				var d Depot
				d, err = NewFileDepot(tempDir)
				require.NoError(t, err)
				return d
			},
			bootstrap: func(t *testing.T) Depot {
				conf := BootstrapDepotConfig{
					FileDepot: tempDir,
					CAName:    "root",
					CAOpts: &CertificateOptions{
						CommonName: "root",
						Expires:    time.Minute,
					},
					ServiceName: "localhost",
					ServiceOpts: &CertificateOptions{
						CommonName: "localhost",
						Host:       "localhost",
						CA:         "root",
					},
				}
				_, err = BootstrapDepot(ctx, conf)
				require.NoError(t, err)
				// We have to do this because the bootstrapped file depot
				// bootstrapped does not include any DepotOptions.
				var d Depot
				d, err = MakeFileDepot(tempDir, DepotOptions{
					CA:                "root",
					DefaultExpiration: time.Minute,
				})
				require.NoError(t, err)
				return d
			},
			check: func(t *testing.T, tag *depot.Tag, data []byte) {
				path := getTagPath(tag)

				if data == nil {
					_, err = os.Stat(filepath.Join(tempDir, path))
					assert.True(t, os.IsNotExist(err))
					return
				}

				var fileData []byte
				fileData, err = ioutil.ReadFile(filepath.Join(tempDir, path))
				require.NoError(t, err)
				assert.Equal(t, data, fileData)
			},
			cleanup: func() {
				require.NoError(t, os.RemoveAll(tempDir))
			},
			tests: []testCase{
				{
					name: "PutFailsWithExisting",
					test: func(t *testing.T, d Depot) {
						const name = "bob"

						assert.NoError(t, d.Put(CrtTag(name), []byte("data")))
						assert.Error(t, d.Put(CrtTag(name), []byte("other data")))

						assert.NoError(t, d.Put(PrivKeyTag(name), []byte("data")))
						assert.Error(t, d.Put(PrivKeyTag(name), []byte("other data")))

						assert.NoError(t, d.Put(CsrTag(name), []byte("data")))
						assert.Error(t, d.Put(CsrTag(name), []byte("other data")))

						assert.NoError(t, d.Put(CrlTag(name), []byte("data")))
						assert.Error(t, d.Put(CrlTag(name), []byte("other data")))
					},
				},
				{
					name: "DeleteWhenDNE",
					test: func(t *testing.T, d Depot) {
						const name = "bob"

						assert.Error(t, d.Delete(CrtTag(name)))
						assert.Error(t, d.Delete(PrivKeyTag(name)))
						assert.Error(t, d.Delete(CsrTag(name)))
						assert.Error(t, d.Delete(CrlTag(name)))
					},
				},
			},
		},
		{
			name: "MongoDB",
			setup: func() Depot {
				return &mongoDepot{
					ctx:            ctx,
					client:         client,
					databaseName:   databaseName,
					collectionName: collectionName,
				}
			},
			bootstrap: func(t *testing.T) Depot {
				conf := BootstrapDepotConfig{
					MongoDepot: &MongoDBOptions{
						DatabaseName:   databaseName,
						CollectionName: collectionName,
						DepotOptions: DepotOptions{
							CA:                "root",
							DefaultExpiration: time.Minute,
						},
					},
					CAName: "root",
					CAOpts: &CertificateOptions{
						CommonName: "root",
						Expires:    time.Minute,
					},
					ServiceName: "localhost",
					ServiceOpts: &CertificateOptions{
						CommonName: "localhost",
						Host:       "localhost",
						CA:         "root",
						Expires:    time.Minute,
					},
				}
				var d Depot
				d, err = BootstrapDepot(ctx, conf)
				require.NoError(t, err)
				return d
			},
			check: func(t *testing.T, tag *depot.Tag, data []byte) {
				var name, key string
				name, key, err = getNameAndKey(tag)
				require.NoError(t, err)

				u := &User{}
				coll := client.Database(databaseName).Collection(collectionName)
				require.NoError(t, coll.FindOne(ctx, bson.M{userIDKey: name}).Decode(u))
				assert.Equal(t, name, u.ID)

				var value string
				switch key {
				case userCertKey:
					value = u.Cert
				case userPrivateKeyKey:
					value = u.PrivateKey
				case userCertReqKey:
					value = u.CertReq
				case userCertRevocListKey:
					value = u.CertRevocList
				}
				assert.Equal(t, string(data), value)
			},
			cleanup: func() {
				require.NoError(t, client.Database(databaseName).Collection(collectionName).Drop(ctx))
			},
			tests: []testCase{
				{
					name: "PutUpdates",
					test: func(t *testing.T, d Depot) {
						coll := client.Database(databaseName).Collection(collectionName)
						const name = "bob"
						user := &User{
							ID:            name,
							Cert:          "cert",
							PrivateKey:    "key",
							CertReq:       "certReq",
							CertRevocList: "certRevocList",
						}
						_, err = coll.InsertOne(ctx, user)
						require.NoError(t, err)
						time.Sleep(time.Second)

						certData := []byte("bob's new fake certificate")
						assert.NoError(t, d.Put(CrtTag(name), certData))
						u := &User{}
						require.NoError(t, coll.FindOne(ctx, bson.M{userIDKey: name}).Decode(u))
						assert.Equal(t, name, u.ID)
						assert.Equal(t, string(certData), u.Cert)
						assert.Equal(t, user.PrivateKey, u.PrivateKey)
						assert.Equal(t, user.CertReq, u.CertReq)
						assert.Equal(t, user.CertRevocList, u.CertRevocList)

						keyData := []byte("bob's new fake private key")
						assert.NoError(t, d.Put(PrivKeyTag(name), keyData))
						u = &User{}
						require.NoError(t, coll.FindOne(ctx, bson.M{userIDKey: name}).Decode(u))
						assert.Equal(t, name, u.ID)
						assert.Equal(t, string(certData), u.Cert)
						assert.Equal(t, string(keyData), u.PrivateKey)
						assert.Equal(t, user.CertReq, u.CertReq)
						assert.Equal(t, user.CertRevocList, u.CertRevocList)

						certReqData := []byte("bob's new fake certificate request")
						assert.NoError(t, d.Put(CsrTag(name), certReqData))
						u = &User{}
						require.NoError(t, coll.FindOne(ctx, bson.M{userIDKey: name}).Decode(u))
						assert.Equal(t, name, u.ID)
						assert.Equal(t, string(certData), u.Cert)
						assert.Equal(t, string(keyData), u.PrivateKey)
						assert.Equal(t, string(certReqData), u.CertReq)
						assert.Equal(t, user.CertRevocList, u.CertRevocList)

						certRevocListData := []byte("bob's new fake certificate revocation list")
						assert.NoError(t, d.Put(CrlTag(name), certRevocListData))
						u = &User{}
						require.NoError(t, coll.FindOne(ctx, bson.M{userIDKey: name}).Decode(u))
						assert.Equal(t, name, u.ID)
						assert.Equal(t, string(certData), u.Cert)
						assert.Equal(t, string(keyData), u.PrivateKey)
						assert.Equal(t, string(certReqData), u.CertReq)
						assert.Equal(t, string(certRevocListData), u.CertRevocList)
					},
				},
				{
					name: "CheckReturnsFalseOnExistingUserWithNoData",
					test: func(t *testing.T, d Depot) {
						const name = "alice"
						u := &User{
							ID: name,
						}
						_, err = client.Database(databaseName).Collection(collectionName).InsertOne(ctx, u)
						require.NoError(t, err)

						assert.False(t, d.Check(CrtTag(name)))
						assert.False(t, d.Check(PrivKeyTag(name)))
						assert.False(t, d.Check(CsrTag(name)))
						assert.False(t, d.Check(CrlTag(name)))
					},
				},
				{
					name: "GetFailsOnExistingUserWithNoData",
					test: func(t *testing.T, d Depot) {
						const name = "bob"
						u := &User{
							ID: name,
						}
						_, err = client.Database(databaseName).Collection(collectionName).InsertOne(ctx, u)
						require.NoError(t, err)

						data, err = d.Get(CrtTag(name))
						assert.Error(t, err)
						assert.Nil(t, data)

						data, err = d.Get(PrivKeyTag(name))
						assert.Error(t, err)
						assert.Nil(t, data)

						data, err = d.Get(CsrTag(name))
						assert.Error(t, err)
						assert.Nil(t, data)

						data, err = d.Get(CrlTag(name))
						assert.Error(t, err)
						assert.Nil(t, data)
					},
				},
				{
					name: "DeleteWhenDNE",
					test: func(t *testing.T, d Depot) {
						const name = "bob"

						assert.NoError(t, d.Delete(CrtTag(name)))
						assert.NoError(t, d.Delete(PrivKeyTag(name)))
						assert.NoError(t, d.Delete(CsrTag(name)))
						assert.NoError(t, d.Delete(CrlTag(name)))
					},
				},
			},
		},
	} {
		t.Run(impl.name, func(t *testing.T) {
			for _, test := range impl.tests {
				t.Run(test.name, func(t *testing.T) {
					d := impl.setup()
					defer impl.cleanup()

					test.test(t, d)
				})
			}
			t.Run("Put", func(t *testing.T) {
				d := impl.setup()
				defer impl.cleanup()
				const name = "bob"

				t.Run("FailsWithNilData", func(t *testing.T) {
					assert.Error(t, d.Put(CrtTag(name), nil))
				})
				t.Run("AddsDataCorrectly", func(t *testing.T) {
					certData := []byte("bob's fake certificate")
					assert.NoError(t, d.Put(CrtTag(name), certData))
					impl.check(t, CrtTag(name), certData)
					impl.check(t, PrivKeyTag(name), nil)
					impl.check(t, CsrTag(name), nil)
					impl.check(t, CrlTag(name), nil)

					keyData := []byte("bob's fake private key")
					assert.NoError(t, d.Put(PrivKeyTag(name), keyData))
					impl.check(t, CrtTag(name), certData)
					impl.check(t, PrivKeyTag(name), keyData)
					impl.check(t, CsrTag(name), nil)
					impl.check(t, CrlTag(name), nil)

					certReqData := []byte("bob's fake certificate request")
					assert.NoError(t, d.Put(CsrTag(name), certReqData))
					impl.check(t, CrtTag(name), certData)
					impl.check(t, PrivKeyTag(name), keyData)
					impl.check(t, CsrTag(name), certReqData)
					impl.check(t, CrlTag(name), nil)

					certRevocListData := []byte("bob's fake certificate revocation list")
					assert.NoError(t, d.Put(CrlTag(name), certRevocListData))
					impl.check(t, CrtTag(name), certData)
					impl.check(t, PrivKeyTag(name), keyData)
					impl.check(t, CsrTag(name), certReqData)
					impl.check(t, CrlTag(name), certRevocListData)
				})
			})
			t.Run("Check", func(t *testing.T) {
				d := impl.setup()
				defer impl.cleanup()
				const name = "alice"

				t.Run("ReturnsFalseWhenDNE", func(t *testing.T) {
					assert.False(t, d.Check(CrtTag(name)))
					assert.False(t, d.Check(PrivKeyTag(name)))
					assert.False(t, d.Check(CsrTag(name)))
					assert.False(t, d.Check(CrlTag(name)))
				})
				t.Run("ReturnsTrueForCorrectTag", func(t *testing.T) {
					data := []byte("alice's fake certificate")
					assert.NoError(t, d.Put(CrtTag(name), data))
					assert.True(t, d.Check(CrtTag(name)))
					assert.False(t, d.Check(PrivKeyTag(name)))
					assert.False(t, d.Check(CsrTag(name)))
					assert.False(t, d.Check(CrlTag(name)))

					data = []byte("alice's fake private key")
					assert.NoError(t, d.Put(PrivKeyTag(name), data))
					assert.True(t, d.Check(CrtTag(name)))
					assert.True(t, d.Check(PrivKeyTag(name)))
					assert.False(t, d.Check(CsrTag(name)))
					assert.False(t, d.Check(CrlTag(name)))

					data = []byte("alice's fake certificate request")
					assert.NoError(t, d.Put(CsrTag(name), data))
					assert.True(t, d.Check(CrtTag(name)))
					assert.True(t, d.Check(PrivKeyTag(name)))
					assert.True(t, d.Check(CsrTag(name)))
					assert.False(t, d.Check(CrlTag(name)))

					data = []byte("alice's fake certificate revocation list")
					assert.NoError(t, d.Put(CrlTag(name), data))
					assert.True(t, d.Check(CrtTag(name)))
					assert.True(t, d.Check(PrivKeyTag(name)))
					assert.True(t, d.Check(CsrTag(name)))
					assert.True(t, d.Check(CrlTag(name)))
				})
			})
			t.Run("Get", func(t *testing.T) {
				d := impl.setup()
				defer impl.cleanup()
				const name = "bob"

				t.Run("FailsWhenDNE", func(t *testing.T) {
					data, err := d.Get(CrtTag(name))
					assert.Error(t, err)
					assert.Nil(t, data)

					data, err = d.Get(PrivKeyTag(name))
					assert.Error(t, err)
					assert.Nil(t, data)

					data, err = d.Get(CsrTag(name))
					assert.Error(t, err)
					assert.Nil(t, data)

					data, err = d.Get(CrlTag(name))
					assert.Error(t, err)
					assert.Nil(t, data)
				})
				t.Run("ReturnsCorrectData", func(t *testing.T) {
					certData := []byte("bob's fake certificate")
					assert.NoError(t, d.Put(CrtTag(name), certData))
					data, err := d.Get(CrtTag(name))
					assert.NoError(t, err)
					assert.Equal(t, certData, data)

					keyData := []byte("bob's fake private key")
					assert.NoError(t, d.Put(PrivKeyTag(name), keyData))
					data, err = d.Get(PrivKeyTag(name))
					assert.NoError(t, err)
					assert.Equal(t, keyData, data)

					certReqData := []byte("bob's fake certificate request")
					assert.NoError(t, d.Put(CsrTag(name), certReqData))
					data, err = d.Get(CsrTag(name))
					assert.NoError(t, err)
					assert.Equal(t, certReqData, data)

					certRevocListData := []byte("bob's fake certificate revocation list")
					assert.NoError(t, d.Put(CrlTag(name), certRevocListData))
					data, err = d.Get(CrlTag(name))
					assert.NoError(t, err)
					assert.Equal(t, certRevocListData, data)
				})
			})
			t.Run("Delete", func(t *testing.T) {
				d := impl.setup()
				defer impl.cleanup()
				const deleteName = "alice"
				const name = "bob"

				certData := []byte("alice's fake certificate")
				keyData := []byte("alice's fake private key")
				certReqData := []byte("alice's fake certificate request")
				certRevocListData := []byte("alice's fake certificate revocation list")
				require.NoError(t, d.Put(CrtTag(deleteName), certData))
				require.NoError(t, d.Put(PrivKeyTag(deleteName), keyData))
				require.NoError(t, d.Put(CsrTag(deleteName), certReqData))
				require.NoError(t, d.Put(CrlTag(deleteName), certRevocListData))

				data := []byte("bob's data")
				require.NoError(t, d.Put(CrtTag(name), data))
				require.NoError(t, d.Put(PrivKeyTag(name), data))
				require.NoError(t, d.Put(CsrTag(name), data))
				require.NoError(t, d.Put(CrlTag(name), data))

				t.Run("RemovesCorrectData", func(t *testing.T) {
					assert.NoError(t, d.Delete(CrtTag(deleteName)))
					impl.check(t, CrtTag(deleteName), nil)
					impl.check(t, PrivKeyTag(deleteName), keyData)
					impl.check(t, CsrTag(deleteName), certReqData)
					impl.check(t, CrlTag(deleteName), certRevocListData)
					impl.check(t, CrtTag(name), data)
					impl.check(t, PrivKeyTag(name), data)
					impl.check(t, CsrTag(name), data)
					impl.check(t, CrlTag(name), data)

					assert.NoError(t, d.Delete(PrivKeyTag(deleteName)))
					impl.check(t, CrtTag(deleteName), nil)
					impl.check(t, PrivKeyTag(deleteName), nil)
					impl.check(t, CsrTag(deleteName), certReqData)
					impl.check(t, CrlTag(deleteName), certRevocListData)
					impl.check(t, CrtTag(name), data)
					impl.check(t, PrivKeyTag(name), data)
					impl.check(t, CsrTag(name), data)
					impl.check(t, CrlTag(name), data)

					assert.NoError(t, d.Delete(CsrTag(deleteName)))
					impl.check(t, CrtTag(deleteName), nil)
					impl.check(t, PrivKeyTag(deleteName), nil)
					impl.check(t, CsrTag(deleteName), nil)
					impl.check(t, CrlTag(deleteName), certRevocListData)
					impl.check(t, CrtTag(name), data)
					impl.check(t, PrivKeyTag(name), data)
					impl.check(t, CsrTag(name), data)
					impl.check(t, CrlTag(name), data)

					assert.NoError(t, d.Delete(CrlTag(deleteName)))
					impl.check(t, CrtTag(deleteName), nil)
					impl.check(t, PrivKeyTag(deleteName), nil)
					impl.check(t, CsrTag(deleteName), nil)
					impl.check(t, CrlTag(deleteName), nil)
					impl.check(t, CrtTag(name), data)
					impl.check(t, PrivKeyTag(name), data)
					impl.check(t, CsrTag(name), data)
					impl.check(t, CrlTag(name), data)
				})
			})
			t.Run("Generate", func(t *testing.T) {
				_ = impl.setup()
				d := impl.bootstrap(t)
				defer impl.cleanup()
				const name = "alice"

				t.Run("FailsWithInvalidName", func(t *testing.T) {
					creds, err := d.Generate("")
					assert.Error(t, err)
					assert.Zero(t, creds)
				})
				t.Run("GeneratesCertificateInMemory", func(t *testing.T) {
					creds, err := d.Generate(name)
					require.NoError(t, err)
					assert.NotZero(t, creds)
					assert.Equal(t, name, creds.ServerName)

					data, err := d.Get(CsrTag(name))
					assert.Error(t, err)
					assert.Zero(t, data)

					data, err = d.Get(CrtTag(name))
					assert.Error(t, err)
					assert.Zero(t, data)
				})
			})
			t.Run("GenerateWithOptions", func(t *testing.T) {
				_ = impl.setup()
				d := impl.bootstrap(t)
				defer impl.cleanup()
				const name = "alice"

				t.Run("FailsWithZeroOptions", func(t *testing.T) {
					creds, err := d.GenerateWithOptions(CertificateOptions{})
					assert.Error(t, err)
					assert.Zero(t, creds)
				})
				t.Run("GeneratesCertificateInMemory", func(t *testing.T) {
					creds, err := d.GenerateWithOptions(CertificateOptions{
						CommonName: name,
						Host:       name,
					})
					require.NoError(t, err)
					assert.NotZero(t, creds)
					assert.Equal(t, name, creds.ServerName)

					data, err := d.Get(CsrTag(name))
					assert.Error(t, err)
					assert.Zero(t, data)

					data, err = d.Get(CrtTag(name))
					assert.Error(t, err)
					assert.Zero(t, data)
				})
			})
		})
	}
}
