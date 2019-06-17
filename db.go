package certdepot

import (
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

// PutTTL sets the TTL to the given expiration time for the name. If the name is
// not found in the collection, this will error.
func (m *mongoDepot) PutTTL(name string, expiration time.Time) error {
	formattedName := formatDepotName(name)
	updateRes, err := m.client.Database(m.databaseName).Collection(m.collectionName).UpdateOne(m.ctx,
		bson.D{{Key: userIDKey, Value: formattedName}},
		bson.M{"$set": bson.M{userTTLKey: expiration}})
	if err != nil {
		return errors.Wrap(err, "problem updating TTL in the database")
	}
	if updateRes.ModifiedCount == 0 {
		return errors.Errorf("update did not change TTL for user %s", name)
	}
	return nil
}

// FindExpiresBefore finds all Users that expire before the given cutoff time.
func (m *mongoDepot) FindExpiresBefore(cutoff time.Time) ([]User, error) {
	users := []User{}
	res, err := m.client.Database(m.databaseName).Collection(m.collectionName).
		Find(m.ctx, expiresBeforeQuery(cutoff))
	if err != nil {
		return nil, errors.Wrap(err, "problem finding expired users")
	}
	if err := res.All(m.ctx, &users); err != nil {
		return nil, errors.Wrap(err, "problem decoding results")
	}

	return users, nil
}

// DeleteExpiresBefore removes all Users that expire before the given cutoff
// time.
func (m *mongoDepot) DeleteExpiresBefore(cutoff time.Time) error {
	_, err := m.client.Database(m.databaseName).Collection(m.collectionName).
		DeleteMany(m.ctx, expiresBeforeQuery(cutoff))
	if err != nil {
		return errors.Wrap(err, "problem removing expired users")
	}
	return nil
}

func expiresBeforeQuery(cutoff time.Time) bson.M {
	return bson.M{userTTLKey: bson.M{"$lte": cutoff}}
}

// FindUser finds the User in the database by name.
func (m *mongoDepot) FindUser(name string) (*User, error) {
	formattedName := formatDepotName(name)
	user := &User{}
	if err := m.client.Database(m.databaseName).Collection(m.collectionName).
		FindOne(m.ctx, bson.D{{Key: userIDKey, Value: formattedName}}).Decode(user); err != nil {
		return nil, errors.Wrap(err, "problem finding user")
	}
	return user, nil
}

// DeleteUser removes a User from the database by name.
func (m *mongoDepot) DeleteUser(name string) error {
	formattedName := formatDepotName(name)
	res, err := m.client.Database(m.databaseName).Collection(m.collectionName).DeleteOne(m.ctx,
		bson.D{{Key: userIDKey, Value: formattedName}})
	if err != nil {
		return errors.Wrap(err, "problem deleting user")
	}
	if res.DeletedCount == 0 {
		return errors.Errorf("could not find user %s to delete", name)
	}
	return nil
}

func formatDepotName(name string) string {
	return strings.Replace(name, " ", "_", -1)
}
