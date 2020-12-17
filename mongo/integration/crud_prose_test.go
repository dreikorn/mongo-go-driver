// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package integration

import (
	"bytes"
	"testing"

	"github.com/dreikorn/mongo-go-driver/bson"
	"github.com/dreikorn/mongo-go-driver/internal/testutil/assert"
	"github.com/dreikorn/mongo-go-driver/mongo"
	"github.com/dreikorn/mongo-go-driver/mongo/integration/mtest"
	"github.com/dreikorn/mongo-go-driver/mongo/options"
	"github.com/dreikorn/mongo-go-driver/mongo/readpref"
)

func TestAggregateSecondaryPreferredReadPreference(t *testing.T) {
	// Use secondaryPreferred instead of secondary because sharded clusters started up by mongo-orchestration have
	// one-node shards, so a secondary read preference is not satisfiable.
	secondaryPrefClientOpts := options.Client().
		SetWriteConcern(mtest.MajorityWc).
		SetReadPreference(readpref.SecondaryPreferred()).
		SetReadConcern(mtest.MajorityRc)
	mtOpts := mtest.NewOptions().
		ClientOptions(secondaryPrefClientOpts).
		MinServerVersion("4.1.0") // Consistent with tests in aggregate-out-readConcern.json

	mt := mtest.New(t, mtOpts)
	mt.Run("aggregate $out with read preference secondary", func(mt *mtest.T) {
		doc, err := bson.Marshal(bson.D{
			{"_id", 1},
			{"x", 11},
		})
		assert.Nil(mt, err, "Marshal error: %v", err)
		_, err = mt.Coll.InsertOne(mtest.Background, doc)
		assert.Nil(mt, err, "InsertOne error: %v", err)

		mt.ClearEvents()
		outputCollName := "aggregate-read-pref-secondary-output"
		outStage := bson.D{
			{"$out", outputCollName},
		}
		cursor, err := mt.Coll.Aggregate(mtest.Background, mongo.Pipeline{outStage})
		assert.Nil(mt, err, "Aggregate error: %v", err)
		_ = cursor.Close(mtest.Background)

		// Assert that the output collection contains the document we expect.
		outputColl := mt.CreateCollection(mtest.Collection{Name: outputCollName}, false)
		cursor, err = outputColl.Find(mtest.Background, bson.D{})
		assert.Nil(mt, err, "Find error: %v", err)
		defer cursor.Close(mtest.Background)

		assert.True(mt, cursor.Next(mtest.Background), "expected Next to return true, got false")
		assert.True(mt, bytes.Equal(doc, cursor.Current), "expected document %s, got %s", bson.Raw(doc), cursor.Current)
		assert.False(mt, cursor.Next(mtest.Background), "unexpected document returned by Find: %s", cursor.Current)

		// Assert that no read preference was sent to the server.
		evt := mt.GetStartedEvent()
		assert.Equal(mt, "aggregate", evt.CommandName, "expected command 'aggregate', got '%s'", evt.CommandName)
		_, err = evt.Command.LookupErr("$readPreference")
		assert.NotNil(mt, err, "expected command %s to not contain $readPreference", evt.Command)
	})
}

func TestErrorsCodeNamePropagated(t *testing.T) {
	// Ensure the codeName field is propagated for both command and write concern errors.

	mtOpts := mtest.NewOptions().
		Topologies(mtest.ReplicaSet).
		CreateClient(false)
	mt := mtest.New(t, mtOpts)
	defer mt.Close()

	mt.RunOpts("command error", mtest.NewOptions().MinServerVersion("3.4"), func(mt *mtest.T) {
		// codeName is propagated in an ok:0 error.

		cmd := bson.D{
			{"insert", mt.Coll.Name()},
			{"documents", []bson.D{}},
		}
		err := mt.DB.RunCommand(mtest.Background, cmd).Err()
		assert.NotNil(mt, err, "expected RunCommand error, got nil")

		ce, ok := err.(mongo.CommandError)
		assert.True(mt, ok, "expected error of type %T, got %v of type %T", mongo.CommandError{}, err, err)
		expectedCodeName := "InvalidLength"
		assert.Equal(mt, expectedCodeName, ce.Name, "expected error code name %q, got %q", expectedCodeName, ce.Name)
	})

	wcCollOpts := options.Collection().
		SetWriteConcern(impossibleWc)
	wcMtOpts := mtest.NewOptions().
		CollectionOptions(wcCollOpts)
	mt.RunOpts("write concern error", wcMtOpts, func(mt *mtest.T) {
		// codeName is propagated for write concern errors.

		_, err := mt.Coll.InsertOne(mtest.Background, bson.D{})
		assert.NotNil(mt, err, "expected InsertOne error, got nil")

		we, ok := err.(mongo.WriteException)
		assert.True(mt, ok, "expected error of type %T, got %v of type %T", mongo.WriteException{}, err, err)
		wce := we.WriteConcernError
		assert.NotNil(mt, wce, "expected write concern error, got %v", we)

		var expectedCodeName string
		if codeNameVal, err := mt.GetSucceededEvent().Reply.LookupErr("writeConcernError", "codeName"); err == nil {
			expectedCodeName = codeNameVal.StringValue()
		}

		assert.Equal(mt, expectedCodeName, wce.Name, "expected code name %q, got %q", expectedCodeName, wce.Name)
	})
}
