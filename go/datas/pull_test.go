// Copyright 2016 The Noms Authors. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package datas

import (
	"testing"

	"github.com/attic-labs/noms/go/chunks"
	"github.com/attic-labs/noms/go/types"
	"github.com/attic-labs/testify/suite"
)

const dsID = "ds1"

func TestLocalToLocalPulls(t *testing.T) {
	suite.Run(t, &LocalToLocalSuite{})
}

func TestRemoteToLocalPulls(t *testing.T) {
	suite.Run(t, &RemoteToLocalSuite{})
}

func TestLocalToRemotePulls(t *testing.T) {
	suite.Run(t, &LocalToRemoteSuite{})
}

func TestRemoteToRemotePulls(t *testing.T) {
	suite.Run(t, &RemoteToRemoteSuite{})
}

type PullSuite struct {
	suite.Suite
	sinkCS   *chunks.TestStore
	sourceCS *chunks.TestStore
	sink     Database
	source   Database
}

type LocalToLocalSuite struct {
	PullSuite
}

func (suite *LocalToLocalSuite) SetupTest() {
	suite.sinkCS = chunks.NewTestStore()
	suite.sourceCS = chunks.NewTestStore()
	suite.sink = NewDatabase(suite.sinkCS)
	suite.source = NewDatabase(suite.sourceCS)
}

type RemoteToLocalSuite struct {
	PullSuite
}

func (suite *RemoteToLocalSuite) SetupTest() {
	suite.sinkCS = chunks.NewTestStore()
	suite.sourceCS = chunks.NewTestStore()
	suite.sink = NewDatabase(suite.sinkCS)
	suite.source = makeRemoteDb(suite.sourceCS)
}

type LocalToRemoteSuite struct {
	PullSuite
}

func (suite *LocalToRemoteSuite) SetupTest() {
	suite.sinkCS = chunks.NewTestStore()
	suite.sourceCS = chunks.NewTestStore()
	suite.sink = makeRemoteDb(suite.sinkCS)
	suite.source = NewDatabase(suite.sourceCS)
}

type RemoteToRemoteSuite struct {
	PullSuite
}

func (suite *RemoteToRemoteSuite) SetupTest() {
	suite.sinkCS = chunks.NewTestStore()
	suite.sourceCS = chunks.NewTestStore()
	suite.sink = makeRemoteDb(suite.sinkCS)
	suite.source = makeRemoteDb(suite.sourceCS)
}

func makeRemoteDb(cs chunks.ChunkStore) Database {
	hbs := newHTTPBatchStoreForTest(cs)
	return &RemoteDatabaseClient{newDatabaseCommon(newCachingChunkHaver(hbs), types.NewValueStore(hbs), hbs)}
}

func (suite *PullSuite) sinkIsLocal() bool {
	_, isLocal := suite.sink.(*LocalDatabase)
	return isLocal
}

func (suite *PullSuite) TearDownTest() {
	suite.sink.Close()
	suite.source.Close()
	suite.sinkCS.Close()
	suite.sourceCS.Close()
}

// Source: -3-> C(L2) -1-> N
//                 \  -2-> L1 -1-> N
//                          \ -1-> L0
//
// Sink: Nada
func (suite *PullSuite) TestPullEverything() {
	l := buildListOfHeight(2, suite.source)
	sourceRef := suite.commitToSource(l, types.NewSet())

	Pull(suite.source, suite.sink, sourceRef, types.Ref{}, 2)
	suite.Equal(0, suite.sinkCS.Reads)

	suite.sink.batchStore().Flush()
	v := suite.sink.ReadValue(sourceRef.TargetHash()).(types.Struct)
	suite.NotNil(v)
	suite.True(l.Equals(v.Get(ValueField)))
}

// Source: -6-> C3(L5) -1-> N
//               .  \  -5-> L4 -1-> N
//                .          \ -4-> L3 -1-> N
//                 .                 \  -3-> L2 -1-> N
//                  5                         \ -2-> L1 -1-> N
//                   .                                \ -1-> L0
//                  C2(L4) -1-> N
//                   .  \  -4-> L3 -1-> N
//                    .          \ -3-> L2 -1-> N
//                     .                 \ -2-> L1 -1-> N
//                      3                        \ -1-> L0
//                       .
//                     C1(L2) -1-> N
//                         \  -2-> L1 -1-> N
//                                  \ -1-> L0
//
// Sink: -3-> C1(L2) -1-> N
//                \  -2-> L1 -1-> N
//                         \ -1-> L0
func (suite *PullSuite) TestPullMultiGeneration() {
	sinkL := buildListOfHeight(2, suite.sink)
	sinkRef := suite.commitToSink(sinkL, types.NewSet())
	expectedReads := suite.sinkCS.Reads

	srcL := buildListOfHeight(2, suite.source)
	sourceRef := suite.commitToSource(srcL, types.NewSet())
	srcL = buildListOfHeight(4, suite.source)
	sourceRef = suite.commitToSource(srcL, types.NewSet(sourceRef))
	srcL = buildListOfHeight(5, suite.source)
	sourceRef = suite.commitToSource(srcL, types.NewSet(sourceRef))

	Pull(suite.source, suite.sink, sourceRef, sinkRef, 2)
	if suite.sinkIsLocal() {
		// C1 gets read from most-local DB
		expectedReads++
	}
	suite.Equal(expectedReads, suite.sinkCS.Reads)

	suite.sink.batchStore().Flush()
	v := suite.sink.ReadValue(sourceRef.TargetHash()).(types.Struct)
	suite.NotNil(v)
	suite.True(srcL.Equals(v.Get(ValueField)))
}

// Source: -6-> C2(L5) -1-> N
//               .  \  -5-> L4 -1-> N
//                .          \ -4-> L3 -1-> N
//                 .                 \  -3-> L2 -1-> N
//                  4                         \ -2-> L1 -1-> N
//                   .                                \ -1-> L0
//                  C1(L3) -1-> N
//                      \  -3-> L2 -1-> N
//                               \ -2-> L1 -1-> N
//                                       \ -1-> L0
//
// Sink: -5-> C3(L3') -1-> N
//             .   \ -3-> L2 -1-> N
//              .   \      \ -2-> L1 -1-> N
//               .   \             \ -1-> L0
//                .   \  - "oy!"
//                 4
//                  .
//                C1(L3) -1-> N
//                    \  -3-> L2 -1-> N
//                             \ -2-> L1 -1-> N
//                                     \ -1-> L0
func (suite *PullSuite) TestPullDivergentHistory() {
	sinkL := buildListOfHeight(3, suite.sink)
	sinkRef := suite.commitToSink(sinkL, types.NewSet())
	srcL := buildListOfHeight(3, suite.source)
	sourceRef := suite.commitToSource(srcL, types.NewSet())

	sinkL = sinkL.Append(types.NewString("oy!"))
	sinkRef = suite.commitToSink(sinkL, types.NewSet(sinkRef))
	srcL = srcL.Set(1, buildListOfHeight(5, suite.source))
	sourceRef = suite.commitToSource(srcL, types.NewSet(sourceRef))
	preReads := suite.sinkCS.Reads

	Pull(suite.source, suite.sink, sourceRef, sinkRef, 2)

	// No objects read from sink, since sink Head is not an ancestor of source HEAD.
	suite.Equal(preReads, suite.sinkCS.Reads)

	suite.sink.batchStore().Flush()
	v := suite.sink.ReadValue(sourceRef.TargetHash()).(types.Struct)
	suite.NotNil(v)
	suite.True(srcL.Equals(v.Get(ValueField)))
}

// Source: -6-> C2(L4) -1-> N
//               .  \  -4-> L3 -1-> N
//                 .         \ -3-> L2 -1-> N
//                  .                \ - "oy!"
//                   5                \ -2-> L1 -1-> N
//                    .                       \ -1-> L0
//                   C1(L4) -1-> N
//                       \  -4-> L3 -1-> N
//                                \ -3-> L2 -1-> N
//                                        \ -2-> L1 -1-> N
//                                                \ -1-> L0
// Sink: -5-> C1(L4) -1-> N
//                \  -4-> L3 -1-> N
//                         \ -3-> L2 -1-> N
//                                 \ -2-> L1 -1-> N
//                                         \ -1-> L0
func (suite *PullSuite) TestPullUpdates() {
	sinkL := buildListOfHeight(4, suite.sink)
	sinkRef := suite.commitToSink(sinkL, types.NewSet())
	expectedReads := suite.sinkCS.Reads

	srcL := buildListOfHeight(4, suite.source)
	sourceRef := suite.commitToSource(srcL, types.NewSet())
	L3 := srcL.Get(1).(types.Ref).TargetValue(suite.source).(types.List)
	L2 := L3.Get(1).(types.Ref).TargetValue(suite.source).(types.List)
	L2 = L2.Append(suite.source.WriteValue(types.NewString("oy!")))
	L3 = L3.Set(1, suite.source.WriteValue(L2))
	srcL = srcL.Set(1, suite.source.WriteValue(L3))
	sourceRef = suite.commitToSource(srcL, types.NewSet(sourceRef))

	Pull(suite.source, suite.sink, sourceRef, sinkRef, 2)

	if suite.sinkIsLocal() {
		// 3 objects read from sink: L3, L2 and C1 (when considering the shared commit).
		expectedReads += 3
	}
	suite.Equal(expectedReads, suite.sinkCS.Reads)

	suite.sink.batchStore().Flush()
	v := suite.sink.ReadValue(sourceRef.TargetHash()).(types.Struct)
	suite.NotNil(v)
	suite.True(srcL.Equals(v.Get(ValueField)))
}

func (suite *PullSuite) commitToSource(v types.Value, p types.Set) types.Ref {
	var err error
	suite.source, err = suite.source.Commit(dsID, NewCommit().Set(ValueField, v).Set(ParentsField, p))
	suite.NoError(err)
	return suite.source.HeadRef(dsID)
}

func (suite *PullSuite) commitToSink(v types.Value, p types.Set) types.Ref {
	var err error
	suite.sink, err = suite.sink.Commit(dsID, NewCommit().Set(ValueField, v).Set(ParentsField, p))
	suite.NoError(err)
	return suite.sink.HeadRef(dsID)
}

func buildListOfHeight(height int, vw types.ValueWriter) types.List {
	unique := 0
	l := types.NewList(types.Number(unique), types.Number(unique+1))
	unique += 2

	for i := 0; i < height; i++ {
		r1, r2 := vw.WriteValue(types.Number(unique)), vw.WriteValue(l)
		unique++
		l = types.NewList(r1, r2)
	}
	return l
}