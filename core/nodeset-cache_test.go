// Copyright 2018 The dexon-consensus-core Authors
// This file is part of the dexon-consensus-core library.
//
// The dexon-consensus-core library is free software: you can redistribute it
// and/or modify it under the terms of the GNU Lesser General Public License as
// published by the Free Software Foundation, either version 3 of the License,
// or (at your option) any later version.
//
// The dexon-consensus-core library is distributed in the hope that it will be
// useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser
// General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the dexon-consensus-core library. If not, see
// <http://www.gnu.org/licenses/>.

package core

import (
	"testing"

	"github.com/dexon-foundation/dexon-consensus-core/core/crypto"
	"github.com/dexon-foundation/dexon-consensus-core/core/crypto/ecdsa"
	"github.com/dexon-foundation/dexon-consensus-core/core/types"
	"github.com/stretchr/testify/suite"
)

type testGov struct {
	s       *NodeSetCacheTestSuite
	curKeys []crypto.PublicKey
}

func (g *testGov) GetConfiguration(round uint64) (cfg *types.Config) { return }
func (g *testGov) GetCRS(round uint64) (b []byte)                    { return }
func (g *testGov) GetNodeSet(round uint64) []crypto.PublicKey {
	// Randomly generating keys, and check them for verification.
	g.curKeys = []crypto.PublicKey{}
	for i := 0; i < 10; i++ {
		prvKey, err := ecdsa.NewPrivateKey()
		g.s.Require().NoError(err)
		g.curKeys = append(g.curKeys, prvKey.PublicKey())
	}
	return g.curKeys
}
func (g *testGov) ProposeThresholdSignature(
	round uint64, signature crypto.Signature) {
}
func (g *testGov) GetThresholdSignature(
	round uint64) (sig crypto.Signature, exists bool) {
	return
}
func (g *testGov) AddDKGComplaint(complaint *types.DKGComplaint) {}
func (g *testGov) DKGComplaints(
	round uint64) (cs []*types.DKGComplaint) {
	return
}
func (g *testGov) AddDKGMasterPublicKey(
	masterPublicKey *types.DKGMasterPublicKey) {
}
func (g *testGov) DKGMasterPublicKeys(
	round uint64) (keys []*types.DKGMasterPublicKey) {
	return
}

type NodeSetCacheTestSuite struct {
	suite.Suite
}

func (s *NodeSetCacheTestSuite) TestBasicUsage() {
	var (
		gov   = &testGov{s: s}
		cache = NewNodeSetCache(gov)
		req   = s.Require()
	)

	chk := func(
		cache *NodeSetCache, round uint64, nodeSet map[types.NodeID]struct{}) {

		for nID := range nodeSet {
			// It should exists.
			exists, err := cache.Exists(round, nID)
			req.NoError(err)
			req.True(exists)
			// We could get keys.
			key, exists := cache.GetPublicKey(nID)
			req.NotNil(key)
			req.True(exists)
		}
	}

	// Try to get round 0.
	nodeSet0, err := cache.GetNodeIDs(0)
	req.NoError(err)
	chk(cache, 0, nodeSet0)
	// Try to get round 1.
	nodeSet1, err := cache.GetNodeIDs(1)
	req.NoError(err)
	chk(cache, 0, nodeSet0)
	chk(cache, 1, nodeSet1)
	// Try to get round 6, round 0 should be purged.
	nodeSet6, err := cache.GetNodeIDs(6)
	req.NoError(err)
	chk(cache, 1, nodeSet1)
	chk(cache, 6, nodeSet6)
	for nID := range nodeSet0 {
		_, exists := cache.GetPublicKey(nID)
		req.False(exists)
	}
}

func TestNodeSetCache(t *testing.T) {
	suite.Run(t, new(NodeSetCacheTestSuite))
}