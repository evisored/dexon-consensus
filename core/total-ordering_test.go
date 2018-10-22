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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dexon-foundation/dexon-consensus-core/common"
	"github.com/dexon-foundation/dexon-consensus-core/core/blockdb"
	"github.com/dexon-foundation/dexon-consensus-core/core/test"
	"github.com/dexon-foundation/dexon-consensus-core/core/types"
	"github.com/stretchr/testify/suite"
)

type TotalOrderingTestSuite struct {
	suite.Suite
}

func (s *TotalOrderingTestSuite) genGenesisBlock(
	vIDs types.NodeIDs,
	chainID uint32,
	acks common.Hashes) *types.Block {

	return &types.Block{
		ProposerID: vIDs[chainID],
		ParentHash: common.Hash{},
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  0,
			ChainID: chainID,
		},
		Acks: common.NewSortedHashes(acks),
	}
}

func (s *TotalOrderingTestSuite) performOneRun(
	to *totalOrdering, revealer test.Revealer) (revealed, ordered string) {
	revealer.Reset()
	curRound := uint64(0)
	for {
		// Reveal next block.
		b, err := revealer.Next()
		if err != nil {
			if err == blockdb.ErrIterationFinished {
				err = nil
				break
			}
		}
		s.Require().NoError(err)
		revealed += b.Hash.String() + ","
		// Perform total ordering.
		blocks, _, err := to.processBlock(&b)
		s.Require().NoError(err)
		for _, b := range blocks {
			ordered += b.Hash.String() + ","
			// Make sure the round ID is increasing, and no interleave.
			s.Require().True(b.Position.Round >= curRound)
			curRound = b.Position.Round
		}
	}
	return
}

func (s *TotalOrderingTestSuite) checkRandomResult(
	revealingSequence, orderingSequence map[string]struct{}) {
	// Make sure we test at least two different
	// revealing sequence.
	s.True(len(revealingSequence) > 1)
	// Make sure all ordering are equal or prefixed
	// to another one.
	for orderFrom := range orderingSequence {
		s.True(len(orderFrom) > 0)
		for orderTo := range orderingSequence {
			if orderFrom == orderTo {
				continue
			}
			ok := strings.HasPrefix(orderFrom, orderTo) ||
				strings.HasPrefix(orderTo, orderFrom)
			s.True(ok)
		}
	}
}

func (s *TotalOrderingTestSuite) checkNotDeliver(to *totalOrdering, b *types.Block) {
	blocks, mode, err := to.processBlock(b)
	s.Empty(blocks)
	s.Equal(mode, TotalOrderingModeNormal)
	s.Nil(err)
}

func (s *TotalOrderingTestSuite) checkHashSequence(blocks []*types.Block, hashes common.Hashes) {
	sort.Sort(hashes)
	for i, h := range hashes {
		s.Equal(blocks[i].Hash, h)
	}
}

func (s *TotalOrderingTestSuite) checkNotInWorkingSet(
	to *totalOrdering, b *types.Block) {

	s.NotContains(to.pendings, b.Hash)
	s.NotContains(to.acked, b.Hash)
}

func (s *TotalOrderingTestSuite) TestBlockRelation() {
	// This test case would verify if 'acking' and 'acked'
	// accumulated correctly.
	//
	// The DAG used below is:
	//  A <- B <- C
	nodes := test.GenerateRandomNodeIDs(5)
	vID := nodes[0]
	blockA := s.genGenesisBlock(nodes, 0, common.Hashes{})
	blockB := &types.Block{
		ProposerID: vID,
		ParentHash: blockA.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{blockA.Hash}),
	}
	blockC := &types.Block{
		ProposerID: vID,
		ParentHash: blockB.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{blockB.Hash}),
	}

	genesisConfig := &totalOrderingConfig{
		roundBasedConfig: roundBasedConfig{
			roundInterval: 1000 * time.Second,
		},
		k:         1,
		phi:       3,
		numChains: uint32(len(nodes)),
	}
	to := newTotalOrdering(genesisConfig)
	s.checkNotDeliver(to, blockA)
	s.checkNotDeliver(to, blockB)
	s.checkNotDeliver(to, blockC)

	// Check 'acked'.
	ackedA := to.acked[blockA.Hash]
	s.Require().NotNil(ackedA)
	s.Len(ackedA, 2)
	s.Contains(ackedA, blockB.Hash)
	s.Contains(ackedA, blockC.Hash)

	ackedB := to.acked[blockB.Hash]
	s.Require().NotNil(ackedB)
	s.Len(ackedB, 1)
	s.Contains(ackedB, blockC.Hash)

	s.Nil(to.acked[blockC.Hash])
}

func (s *TotalOrderingTestSuite) TestCreateAckingHeightVectorFromHeightVector() {
	var (
		cache   = newTotalOrderingObjectCache(5)
		dirties = []int{0, 1, 2, 3, 4}
	)
	// Prepare global acking status.
	global := &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}

	// For 'not existed' record in local but exist in global,
	// should be infinity.
	candidate := &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 0, count: 2},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}
	candidate.updateAckingHeightVector(global, 0, dirties, cache)
	s.Equal(candidate.cachedHeightVector[0], uint64(0))
	s.Equal(candidate.cachedHeightVector[1], infinity)
	s.Equal(candidate.cachedHeightVector[2], infinity)
	s.Equal(candidate.cachedHeightVector[3], infinity)

	// For local min exceeds global's min+k-1, should be infinity
	candidate = &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 3, count: 1},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}
	candidate.updateAckingHeightVector(global, 2, dirties, cache)
	s.Equal(candidate.cachedHeightVector[0], infinity)
	candidate.updateAckingHeightVector(global, 3, dirties, cache)
	s.Equal(candidate.cachedHeightVector[0], uint64(3))

	candidate = &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 0, count: 3},
			&totalOrderingHeightRecord{minHeight: 0, count: 3},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}
	candidate.updateAckingHeightVector(global, 5, dirties, cache)
}

func (s *TotalOrderingTestSuite) TestCreateAckingNodeSetFromHeightVector() {
	global := &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 5},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}

	local := &totalOrderingCandidateInfo{
		ackedStatus: []*totalOrderingHeightRecord{
			&totalOrderingHeightRecord{minHeight: 1, count: 2},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
			&totalOrderingHeightRecord{minHeight: 0, count: 0},
		}}
	s.Equal(local.getAckingNodeSetLength(global, 1, 5), uint64(1))
	s.Equal(local.getAckingNodeSetLength(global, 2, 5), uint64(1))
	s.Equal(local.getAckingNodeSetLength(global, 3, 5), uint64(0))
}

func (s *TotalOrderingTestSuite) TestGrade() {
	// This test case just fake some internal structure used
	// when performing total ordering.
	var (
		nodes      = test.GenerateRandomNodeIDs(5)
		cache      = newTotalOrderingObjectCache(5)
		dirtyNodes = []int{0, 1, 2, 3, 4}
	)
	ansLength := uint64(len(map[types.NodeID]struct{}{
		nodes[0]: struct{}{},
		nodes[1]: struct{}{},
		nodes[2]: struct{}{},
		nodes[3]: struct{}{},
	}))
	candidate1 := newTotalOrderingCandidateInfo(common.Hash{}, cache)
	candidate1.cachedHeightVector = []uint64{
		1, infinity, infinity, infinity, infinity}
	candidate2 := newTotalOrderingCandidateInfo(common.Hash{}, cache)
	candidate2.cachedHeightVector = []uint64{
		1, 1, 1, 1, infinity}
	candidate3 := newTotalOrderingCandidateInfo(common.Hash{}, cache)
	candidate3.cachedHeightVector = []uint64{
		1, 1, infinity, infinity, infinity}

	candidate2.updateWinRecord(
		0, candidate1, dirtyNodes, cache, 5)
	s.Equal(candidate2.winRecords[0].grade(5, 3, ansLength), 1)
	candidate1.updateWinRecord(
		1, candidate2, dirtyNodes, cache, 5)
	s.Equal(candidate1.winRecords[1].grade(5, 3, ansLength), 0)
	candidate2.updateWinRecord(
		2, candidate3, dirtyNodes, cache, 5)
	s.Equal(candidate2.winRecords[2].grade(5, 3, ansLength), -1)
	candidate3.updateWinRecord(
		1, candidate2, dirtyNodes, cache, 5)
	s.Equal(candidate3.winRecords[1].grade(5, 3, ansLength), 0)
}

func (s *TotalOrderingTestSuite) TestCycleDetection() {
	// Make sure we don't get hang by cycle from
	// block's acks.
	nodes := test.GenerateRandomNodeIDs(5)

	// create blocks with cycles in acking relation.
	cycledHash := common.NewRandomHash()
	b00 := s.genGenesisBlock(nodes, 0, common.Hashes{cycledHash})
	b01 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b00.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b00.Hash}),
	}
	b02 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b01.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b01.Hash}),
	}
	b03 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b02.Hash,
		Hash:       cycledHash,
		Position: types.Position{
			Height:  3,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b02.Hash}),
	}

	// Create a block acks self.
	b10 := s.genGenesisBlock(nodes, 1, common.Hashes{})
	b10.Acks = append(b10.Acks, b10.Hash)

	// Make sure we won't hang when cycle exists.
	genesisConfig := &totalOrderingConfig{
		roundBasedConfig: roundBasedConfig{
			roundInterval: 1000 * time.Second,
		},
		k:         1,
		phi:       3,
		numChains: uint32(len(nodes)),
	}
	to := newTotalOrdering(genesisConfig)
	s.checkNotDeliver(to, b00)
	s.checkNotDeliver(to, b01)
	s.checkNotDeliver(to, b02)

	// Should not hang in this line.
	s.checkNotDeliver(to, b03)
	// Should not hang in this line
	s.checkNotDeliver(to, b10)
}

func (s *TotalOrderingTestSuite) TestEarlyDeliver() {
	// The test scenario:
	//
	//  o o o o o
	//  : : : : : <- (K - 1) layers
	//  o o o o o
	//   \ v /  |
	//     o    o
	//     A    B
	//  Even when B is not received, A should
	//  be able to be delivered.
	nodes := test.GenerateRandomNodeIDs(5)
	genesisConfig := &totalOrderingConfig{
		roundBasedConfig: roundBasedConfig{
			roundInterval: 1000 * time.Second,
		},
		k:         2,
		phi:       3,
		numChains: uint32(len(nodes)),
	}
	to := newTotalOrdering(genesisConfig)
	genNextBlock := func(b *types.Block) *types.Block {
		return &types.Block{
			ProposerID: b.ProposerID,
			ParentHash: b.Hash,
			Hash:       common.NewRandomHash(),
			Position: types.Position{
				Height:  b.Position.Height + 1,
				ChainID: b.Position.ChainID,
			},
			Acks: common.NewSortedHashes(common.Hashes{b.Hash}),
		}
	}

	b00 := s.genGenesisBlock(nodes, 0, common.Hashes{})
	b01 := genNextBlock(b00)
	b02 := genNextBlock(b01)

	b10 := s.genGenesisBlock(nodes, 1, common.Hashes{b00.Hash})
	b11 := genNextBlock(b10)
	b12 := genNextBlock(b11)

	b20 := s.genGenesisBlock(nodes, 2, common.Hashes{b00.Hash})
	b21 := genNextBlock(b20)
	b22 := genNextBlock(b21)

	b30 := s.genGenesisBlock(nodes, 3, common.Hashes{b00.Hash})
	b31 := genNextBlock(b30)
	b32 := genNextBlock(b31)

	// It's a valid block sequence to deliver
	// to total ordering algorithm: DAG.
	s.checkNotDeliver(to, b00)
	s.checkNotDeliver(to, b01)
	s.checkNotDeliver(to, b02)

	candidate := to.candidates[0]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight,
		b00.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(3))

	s.checkNotDeliver(to, b10)
	s.checkNotDeliver(to, b11)
	s.checkNotDeliver(to, b12)
	s.checkNotDeliver(to, b20)
	s.checkNotDeliver(to, b21)
	s.checkNotDeliver(to, b22)
	s.checkNotDeliver(to, b30)
	s.checkNotDeliver(to, b31)

	// Check the internal state before delivering.
	s.Len(to.candidateChainMapping, 1) // b00 is the only candidate.

	candidate = to.candidates[0]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b00.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(3))
	s.Equal(candidate.ackedStatus[1].minHeight, b10.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(3))
	s.Equal(candidate.ackedStatus[2].minHeight, b20.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(3))
	s.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(2))

	blocks, mode, err := to.processBlock(b32)
	s.Require().Len(blocks, 1)
	s.Equal(mode, TotalOrderingModeEarly)
	s.Nil(err)
	s.checkHashSequence(blocks, common.Hashes{b00.Hash})

	// Check the internal state after delivered.
	s.Len(to.candidateChainMapping, 4) // b01, b10, b20, b30 are candidates.

	// Check b01.
	candidate = to.candidates[0]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b01.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(2))

	// Check b10.
	candidate = to.candidates[1]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[1].minHeight, b10.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(3))

	// Check b20.
	candidate = to.candidates[2]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[2].minHeight, b20.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(3))

	// Check b30.
	candidate = to.candidates[3]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(3))

	// Make sure b00 doesn't exist in current working set:
	s.checkNotInWorkingSet(to, b00)
}

func (s *TotalOrderingTestSuite) TestBasicCaseForK2() {
	// It's a handcrafted test case.
	nodes := test.GenerateRandomNodeIDs(5)
	genesisConfig := &totalOrderingConfig{
		roundBasedConfig: roundBasedConfig{
			roundInterval: 1000 * time.Second,
		},
		k:         2,
		phi:       3,
		numChains: uint32(len(nodes)),
	}
	to := newTotalOrdering(genesisConfig)
	// Setup blocks.
	b00 := s.genGenesisBlock(nodes, 0, common.Hashes{})
	b10 := s.genGenesisBlock(nodes, 1, common.Hashes{})
	b20 := s.genGenesisBlock(nodes, 2, common.Hashes{b10.Hash})
	b30 := s.genGenesisBlock(nodes, 3, common.Hashes{b20.Hash})
	b40 := s.genGenesisBlock(nodes, 4, common.Hashes{})
	b11 := &types.Block{
		ProposerID: nodes[1],
		ParentHash: b10.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 1,
		},
		Acks: common.NewSortedHashes(common.Hashes{b10.Hash, b00.Hash}),
	}
	b01 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b00.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b00.Hash, b11.Hash}),
	}
	b21 := &types.Block{
		ProposerID: nodes[2],
		ParentHash: b20.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 2,
		},
		Acks: common.NewSortedHashes(common.Hashes{b20.Hash, b01.Hash}),
	}
	b31 := &types.Block{
		ProposerID: nodes[3],
		ParentHash: b30.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 3,
		},
		Acks: common.NewSortedHashes(common.Hashes{b30.Hash, b21.Hash}),
	}
	b02 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b01.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b01.Hash, b21.Hash}),
	}
	b12 := &types.Block{
		ProposerID: nodes[1],
		ParentHash: b11.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 1,
		},
		Acks: common.NewSortedHashes(common.Hashes{b11.Hash, b21.Hash}),
	}
	b32 := &types.Block{
		ProposerID: nodes[3],
		ParentHash: b31.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 3,
		},
		Acks: common.NewSortedHashes(common.Hashes{b31.Hash}),
	}
	b22 := &types.Block{
		ProposerID: nodes[2],
		ParentHash: b21.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 2,
		},
		Acks: common.NewSortedHashes(common.Hashes{b21.Hash, b32.Hash}),
	}
	b23 := &types.Block{
		ProposerID: nodes[2],
		ParentHash: b22.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  3,
			ChainID: 2,
		},
		Acks: common.NewSortedHashes(common.Hashes{b22.Hash}),
	}
	b03 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b02.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  3,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b02.Hash, b22.Hash}),
	}
	b13 := &types.Block{
		ProposerID: nodes[1],
		ParentHash: b12.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  3,
			ChainID: 1,
		},
		Acks: common.NewSortedHashes(common.Hashes{b12.Hash, b22.Hash}),
	}
	b14 := &types.Block{
		ProposerID: nodes[1],
		ParentHash: b13.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  4,
			ChainID: 1,
		},
		Acks: common.NewSortedHashes(common.Hashes{b13.Hash}),
	}
	b41 := &types.Block{
		ProposerID: nodes[4],
		ParentHash: b40.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 4,
		},
		Acks: common.NewSortedHashes(common.Hashes{b40.Hash}),
	}
	b42 := &types.Block{
		ProposerID: nodes[4],
		ParentHash: b41.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  2,
			ChainID: 4,
		},
		Acks: common.NewSortedHashes(common.Hashes{b41.Hash}),
	}

	s.checkNotDeliver(to, b00)
	s.checkNotDeliver(to, b10)
	s.checkNotDeliver(to, b11)
	s.checkNotDeliver(to, b01)
	s.checkNotDeliver(to, b20)
	s.checkNotDeliver(to, b30)
	s.checkNotDeliver(to, b21)
	s.checkNotDeliver(to, b31)
	s.checkNotDeliver(to, b32)
	s.checkNotDeliver(to, b22)
	s.checkNotDeliver(to, b12)

	// Make sure 'acked' for current precedings is correct.
	acked := to.acked[b00.Hash]
	s.Require().NotNil(acked)
	s.Len(acked, 7)
	s.Contains(acked, b01.Hash)
	s.Contains(acked, b11.Hash)
	s.Contains(acked, b12.Hash)
	s.Contains(acked, b21.Hash)
	s.Contains(acked, b22.Hash)
	s.Contains(acked, b31.Hash)
	s.Contains(acked, b32.Hash)

	acked = to.acked[b10.Hash]
	s.Require().NotNil(acked)
	s.Len(acked, 9)
	s.Contains(acked, b01.Hash)
	s.Contains(acked, b11.Hash)
	s.Contains(acked, b12.Hash)
	s.Contains(acked, b20.Hash)
	s.Contains(acked, b21.Hash)
	s.Contains(acked, b22.Hash)
	s.Contains(acked, b30.Hash)
	s.Contains(acked, b31.Hash)
	s.Contains(acked, b32.Hash)

	// Make sure there are 2 candidates.
	s.Require().Len(to.candidateChainMapping, 2)

	// Check b00's height vector.
	candidate := to.candidates[0]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b00.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(2))
	s.Equal(candidate.ackedStatus[1].minHeight, b11.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(2))
	s.Equal(candidate.ackedStatus[2].minHeight, b21.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(2))
	s.Equal(candidate.ackedStatus[3].minHeight, b31.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(2))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	// Check b10's height vector.
	candidate = to.candidates[1]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b01.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(1))
	s.Equal(candidate.ackedStatus[1].minHeight, b10.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(3))
	s.Equal(candidate.ackedStatus[2].minHeight, b20.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(3))
	s.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(3))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	// Check the first deliver.
	blocks, mode, err := to.processBlock(b02)
	s.Equal(mode, TotalOrderingModeEarly)
	s.Nil(err)
	s.checkHashSequence(blocks, common.Hashes{b00.Hash, b10.Hash})

	// Make sure b00, b10 are removed from current working set.
	s.checkNotInWorkingSet(to, b00)
	s.checkNotInWorkingSet(to, b10)

	// Check if candidates of next round are picked correctly.
	s.Len(to.candidateChainMapping, 2)

	// Check b01's height vector.
	candidate = to.candidates[1]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b01.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(2))
	s.Equal(candidate.ackedStatus[1].minHeight, b11.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(2))
	s.Equal(candidate.ackedStatus[2].minHeight, b21.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(2))
	s.Equal(candidate.ackedStatus[3].minHeight, b11.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(2))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	// Check b20's height vector.
	candidate = to.candidates[2]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b02.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(1))
	s.Equal(candidate.ackedStatus[1].minHeight, b12.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(1))
	s.Equal(candidate.ackedStatus[2].minHeight, b20.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(3))
	s.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(3))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	s.checkNotDeliver(to, b13)

	// Check the second deliver.
	blocks, mode, err = to.processBlock(b03)
	s.Equal(mode, TotalOrderingModeEarly)
	s.Nil(err)
	s.checkHashSequence(blocks, common.Hashes{b11.Hash, b20.Hash})

	// Make sure b11, b20 are removed from current working set.
	s.checkNotInWorkingSet(to, b11)
	s.checkNotInWorkingSet(to, b20)

	// Add b40, b41, b42 to pending set.
	s.checkNotDeliver(to, b40)
	s.checkNotDeliver(to, b41)
	s.checkNotDeliver(to, b42)
	s.checkNotDeliver(to, b14)

	// Make sure b01, b30, b40 are candidate in next round.
	s.Len(to.candidateChainMapping, 3)
	candidate = to.candidates[0]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b01.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(3))
	s.Equal(candidate.ackedStatus[1].minHeight, b12.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(3))
	s.Equal(candidate.ackedStatus[2].minHeight, b21.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(2))
	s.Equal(candidate.ackedStatus[3].minHeight, b31.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(2))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	candidate = to.candidates[3]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].minHeight, b03.Position.Height)
	s.Equal(candidate.ackedStatus[0].count, uint64(1))
	s.Equal(candidate.ackedStatus[1].minHeight, b13.Position.Height)
	s.Equal(candidate.ackedStatus[1].count, uint64(2))
	s.Equal(candidate.ackedStatus[2].minHeight, b22.Position.Height)
	s.Equal(candidate.ackedStatus[2].count, uint64(1))
	s.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	s.Equal(candidate.ackedStatus[3].count, uint64(3))
	s.Equal(candidate.ackedStatus[4].count, uint64(0))

	candidate = to.candidates[4]
	s.Require().NotNil(candidate)
	s.Equal(candidate.ackedStatus[0].count, uint64(0))
	s.Equal(candidate.ackedStatus[1].count, uint64(0))
	s.Equal(candidate.ackedStatus[2].count, uint64(0))
	s.Equal(candidate.ackedStatus[3].count, uint64(0))
	s.Equal(candidate.ackedStatus[4].minHeight, b40.Position.Height)
	s.Equal(candidate.ackedStatus[4].count, uint64(3))

	// Make 'Acking Node Set' contains blocks from all chains,
	// this should trigger not-early deliver.
	blocks, mode, err = to.processBlock(b23)
	s.Equal(mode, TotalOrderingModeNormal)
	s.Nil(err)
	s.checkHashSequence(blocks, common.Hashes{b01.Hash, b30.Hash})

	// Make sure b01, b30 not in working set
	s.checkNotInWorkingSet(to, b01)
	s.checkNotInWorkingSet(to, b30)

	// Make sure b21, b40 are candidates of next round.
	s.Equal(to.candidateChainMapping[b21.Position.ChainID], b21.Hash)
	s.Equal(to.candidateChainMapping[b40.Position.ChainID], b40.Hash)
}

func (s *TotalOrderingTestSuite) TestBasicCaseForK0() {
	// This is a relatively simple test for K=0.
	//
	//  0   1   2    3    4
	//  -------------------
	//  .   .   .    .    .
	//  .   .   .    .    .
	//  o   o   o <- o <- o   Height: 1
	//  | \ | \ |    |
	//  v   v   v    v
	//  o   o   o <- o        Height: 0
	var (
		nodes         = test.GenerateRandomNodeIDs(5)
		genesisConfig = &totalOrderingConfig{
			roundBasedConfig: roundBasedConfig{
				roundInterval: 1000 * time.Second,
			},
			k:         0,
			phi:       3,
			numChains: uint32(len(nodes)),
		}
		req = s.Require()
		to  = newTotalOrdering(genesisConfig)
	)
	// Setup blocks.
	b00 := s.genGenesisBlock(nodes, 0, common.Hashes{})
	b10 := s.genGenesisBlock(nodes, 1, common.Hashes{})
	b20 := s.genGenesisBlock(nodes, 2, common.Hashes{})
	b30 := s.genGenesisBlock(nodes, 3, common.Hashes{b20.Hash})
	b01 := &types.Block{
		ProposerID: nodes[0],
		ParentHash: b00.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 0,
		},
		Acks: common.NewSortedHashes(common.Hashes{b00.Hash, b10.Hash}),
	}
	b11 := &types.Block{
		ProposerID: nodes[1],
		ParentHash: b10.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 1,
		},
		Acks: common.NewSortedHashes(common.Hashes{b10.Hash, b20.Hash}),
	}
	b21 := &types.Block{
		ProposerID: nodes[2],
		ParentHash: b20.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 2,
		},
		Acks: common.NewSortedHashes(common.Hashes{b20.Hash}),
	}
	b31 := &types.Block{
		ProposerID: nodes[3],
		ParentHash: b30.Hash,
		Hash:       common.NewRandomHash(),
		Position: types.Position{
			Height:  1,
			ChainID: 3,
		},
		Acks: common.NewSortedHashes(common.Hashes{b21.Hash, b30.Hash}),
	}
	b40 := s.genGenesisBlock(nodes, 4, common.Hashes{b31.Hash})

	s.checkNotDeliver(to, b00)
	s.checkNotDeliver(to, b10)
	s.checkNotDeliver(to, b20)
	s.checkNotDeliver(to, b30)
	s.checkNotDeliver(to, b01)
	s.checkNotDeliver(to, b11)
	s.checkNotDeliver(to, b21)
	s.checkNotDeliver(to, b31)

	// Check candidate status before delivering.
	candidate := to.candidates[0]
	req.NotNil(candidate)
	req.Equal(candidate.ackedStatus[0].minHeight, b00.Position.Height)
	req.Equal(candidate.ackedStatus[0].count, uint64(2))

	candidate = to.candidates[1]
	req.NotNil(candidate)
	req.Equal(candidate.ackedStatus[0].minHeight, b01.Position.Height)
	req.Equal(candidate.ackedStatus[0].count, uint64(1))
	req.Equal(candidate.ackedStatus[1].minHeight, b10.Position.Height)
	req.Equal(candidate.ackedStatus[1].count, uint64(2))

	candidate = to.candidates[2]
	req.NotNil(candidate)
	req.Equal(candidate.ackedStatus[1].minHeight, b11.Position.Height)
	req.Equal(candidate.ackedStatus[1].count, uint64(1))
	req.Equal(candidate.ackedStatus[2].minHeight, b20.Position.Height)
	req.Equal(candidate.ackedStatus[2].count, uint64(2))
	req.Equal(candidate.ackedStatus[3].minHeight, b30.Position.Height)
	req.Equal(candidate.ackedStatus[3].count, uint64(2))

	// This new block should trigger non-early deliver.
	blocks, mode, err := to.processBlock(b40)
	req.Equal(mode, TotalOrderingModeNormal)
	req.Nil(err)
	s.checkHashSequence(blocks, common.Hashes{b20.Hash})

	// Make sure b20 is no long existing in working set.
	s.checkNotInWorkingSet(to, b20)

	// Make sure b10, b30 are candidates for next round.
	req.Equal(to.candidateChainMapping[b00.Position.ChainID], b00.Hash)
	req.Equal(to.candidateChainMapping[b10.Position.ChainID], b10.Hash)
	req.Equal(to.candidateChainMapping[b30.Position.ChainID], b30.Hash)
}

func (s *TotalOrderingTestSuite) baseTestRandomlyGeneratedBlocks(
	totalOrderingConstructor func(chainNum uint32) *totalOrdering,
	chainNum uint32,
	ackingCountGenerator func() int,
	repeat int) {
	var (
		req               = s.Require()
		revealingSequence = make(map[string]struct{})
		orderingSequence  = make(map[string]struct{})
		genesisTime       = time.Now().UTC()
	)
	gen := test.NewBlocksGenerator(&test.BlocksGeneratorConfig{
		NumChains:            chainNum,
		MinBlockTimeInterval: 0,
		MaxBlockTimeInterval: 500 * time.Millisecond,
	}, ackingCountGenerator, hashBlock)
	db, err := blockdb.NewMemBackedBlockDB()
	req.NoError(err)
	req.NoError(gen.Generate(
		0,
		genesisTime,
		genesisTime.Add(20*time.Second),
		db))
	iter, err := db.GetAll()
	req.NoError(err)
	// Setup a revealer that would reveal blocks forming
	// valid DAGs.
	revealer, err := test.NewRandomDAGRevealer(iter)
	req.NoError(err)
	// TODO (mission): make this part run concurrently.
	for i := 0; i < repeat; i++ {
		revealed, ordered := s.performOneRun(
			totalOrderingConstructor(chainNum), revealer)
		revealingSequence[revealed] = struct{}{}
		orderingSequence[ordered] = struct{}{}
	}
	s.checkRandomResult(revealingSequence, orderingSequence)
}

func (s *TotalOrderingTestSuite) TestRandomlyGeneratedBlocks() {
	var (
		numChains = uint32(20)
		phi       = uint64(10)
		repeat    = 15
	)
	if testing.Short() {
		numChains = 10
		phi = 5
		repeat = 3
	}

	ackingCountGenerators := []func() int{
		nil, // Acking frequency with normal distribution.
		test.MaxAckingCountGenerator(0),         // Low acking frequency.
		test.MaxAckingCountGenerator(numChains), // High acking frequency.
	}

	// Test based on different acking frequency.
	for _, gen := range ackingCountGenerators {
		// Test for K=0.
		constructor := func(numChains uint32) *totalOrdering {
			genesisConfig := &totalOrderingConfig{
				roundBasedConfig: roundBasedConfig{
					roundInterval: 1000 * time.Second,
				},
				k:         0,
				phi:       phi,
				numChains: numChains,
			}
			to := newTotalOrdering(genesisConfig)
			// Add config for next round.
			s.Require().NoError(to.appendConfig(1, &types.Config{
				K:         0,
				PhiRatio:  0.5,
				NumChains: numChains,
			}))
			return to
		}
		s.baseTestRandomlyGeneratedBlocks(constructor, numChains, gen, repeat)
		// Test for K=1.
		constructor = func(numChains uint32) *totalOrdering {
			genesisConfig := &totalOrderingConfig{
				roundBasedConfig: roundBasedConfig{
					roundInterval: 1000 * time.Second,
				},
				k:         1,
				phi:       phi,
				numChains: numChains,
			}
			to := newTotalOrdering(genesisConfig)
			// Add config for next round.
			s.Require().NoError(to.appendConfig(1, &types.Config{
				K:         1,
				PhiRatio:  0.5,
				NumChains: numChains,
			}))
			return to
		}
		s.baseTestRandomlyGeneratedBlocks(constructor, numChains, gen, repeat)
		// Test for K=2.
		constructor = func(numChains uint32) *totalOrdering {
			genesisConfig := &totalOrderingConfig{
				roundBasedConfig: roundBasedConfig{
					roundInterval: 1000 * time.Second,
				},
				k:         2,
				phi:       phi,
				numChains: numChains,
			}
			to := newTotalOrdering(genesisConfig)
			s.Require().NoError(to.appendConfig(1, &types.Config{
				K:         2,
				PhiRatio:  0.5,
				NumChains: numChains,
			}))
			return to
		}
		s.baseTestRandomlyGeneratedBlocks(constructor, numChains, gen, repeat)
		// Test for K=3.
		constructor = func(numChains uint32) *totalOrdering {
			genesisConfig := &totalOrderingConfig{
				roundBasedConfig: roundBasedConfig{
					roundInterval: 1000 * time.Second,
				},
				k:         3,
				phi:       phi,
				numChains: numChains,
			}
			to := newTotalOrdering(genesisConfig)
			s.Require().NoError(to.appendConfig(1, &types.Config{
				K:         3,
				PhiRatio:  0.5,
				NumChains: numChains,
			}))
			return to
		}
		s.baseTestRandomlyGeneratedBlocks(constructor, numChains, gen, repeat)
	}
}

func (s *TotalOrderingTestSuite) baseTestForRoundChange(
	repeat int, configs []*types.Config) {
	var (
		req         = s.Require()
		genesisTime = time.Now().UTC()
	)
	db, err := blockdb.NewMemBackedBlockDB()
	req.NoError(err)
	// Generate DAG for rounds.
	// NOTE: the last config won't be tested, just avoid panic
	//       when round switching.
	begin := genesisTime
	for roundID, config := range configs[:len(configs)-1] {
		gen := test.NewBlocksGenerator(
			test.NewBlocksGeneratorConfig(config), nil, hashBlock)
		end := begin.Add(config.RoundInterval)
		req.NoError(gen.Generate(uint64(roundID), begin, end, db))
		begin = end
	}
	// Test, just dump the whole DAG to total ordering and make sure
	// repeating it won't change it delivered sequence.
	iter, err := db.GetAll()
	req.NoError(err)
	revealer, err := test.NewRandomDAGRevealer(iter)
	req.NoError(err)
	revealingSequence := make(map[string]struct{})
	orderingSequence := make(map[string]struct{})
	for i := 0; i < repeat; i++ {
		to := newTotalOrdering(
			newGenesisTotalOrderingConfig(genesisTime, configs[0]))
		for roundID, config := range configs[1:] {
			req.NoError(to.appendConfig(uint64(roundID+1), config))
		}
		revealed, ordered := s.performOneRun(to, revealer)
		revealingSequence[revealed] = struct{}{}
		orderingSequence[ordered] = struct{}{}
	}
	s.checkRandomResult(revealingSequence, orderingSequence)
}

func (s *TotalOrderingTestSuite) TestNumChainsChanged() {
	// This test fixes K, Phi, and changes 'numChains' for each round.
	fix := func(c *types.Config) *types.Config {
		c.K = 1
		c.PhiRatio = 0.5
		c.MinBlockInterval = 0
		c.MaxBlockInterval = 500 * time.Millisecond
		c.RoundInterval = 10 * time.Second
		return c
	}
	var (
		repeat  = 7
		configs = []*types.Config{
			fix(&types.Config{NumChains: 7}),
			fix(&types.Config{NumChains: 10}),
			fix(&types.Config{NumChains: 4}),
			fix(&types.Config{NumChains: 13}),
			fix(&types.Config{NumChains: 4}),
		}
	)
	s.baseTestForRoundChange(repeat, configs)
}

func (s *TotalOrderingTestSuite) TestPhiChanged() {
	// This test fixes K, numChains, and changes Phi each round.
	fix := func(c *types.Config) *types.Config {
		c.K = 1
		c.NumChains = 10
		c.MinBlockInterval = 0
		c.MaxBlockInterval = 500 * time.Millisecond
		c.RoundInterval = 10 * time.Second
		return c
	}
	var (
		repeat  = 7
		configs = []*types.Config{
			fix(&types.Config{PhiRatio: 0.5}),
			fix(&types.Config{PhiRatio: 0.7}),
			fix(&types.Config{PhiRatio: 1}),
			fix(&types.Config{PhiRatio: 0.5}),
			fix(&types.Config{PhiRatio: 0.7}),
		}
	)
	s.baseTestForRoundChange(repeat, configs)
}

func (s *TotalOrderingTestSuite) TestKChanged() {
	// This test fixes phi, numChains, and changes K each round.
	fix := func(c *types.Config) *types.Config {
		c.NumChains = 10
		c.PhiRatio = 0.7
		c.MinBlockInterval = 0
		c.MaxBlockInterval = 500 * time.Millisecond
		c.RoundInterval = 10 * time.Second
		return c
	}
	var (
		repeat  = 7
		configs = []*types.Config{
			fix(&types.Config{K: 0}),
			fix(&types.Config{K: 4}),
			fix(&types.Config{K: 1}),
			fix(&types.Config{K: 2}),
			fix(&types.Config{K: 0}),
		}
	)
	s.baseTestForRoundChange(repeat, configs)
}

func (s *TotalOrderingTestSuite) TestRoundChanged() {
	// This test changes everything when round changed.
	fix := func(c *types.Config) *types.Config {
		c.MinBlockInterval = 0
		c.MaxBlockInterval = 500 * time.Millisecond
		c.RoundInterval = 10 * time.Second
		return c
	}
	var (
		repeat  = 7
		configs = []*types.Config{
			fix(&types.Config{K: 0, NumChains: 4, PhiRatio: 0.5}),
			fix(&types.Config{K: 1, NumChains: 10, PhiRatio: 0.7}),
			fix(&types.Config{K: 2, NumChains: 7, PhiRatio: 0.8}),
			fix(&types.Config{K: 0, NumChains: 4, PhiRatio: 0.5}),
			fix(&types.Config{K: 3, NumChains: 10, PhiRatio: 0.8}),
			fix(&types.Config{K: 0, NumChains: 7, PhiRatio: 0.5}),
			fix(&types.Config{K: 2, NumChains: 13, PhiRatio: 0.7}),
		}
	)
	s.baseTestForRoundChange(repeat, configs)
}

func (s *TotalOrderingTestSuite) TestRunFromNonGenesis() {
	var (
		req         = s.Require()
		numChains   = uint32(19)
		genesisTime = time.Now().UTC()
	)
	gen := test.NewBlocksGenerator(&test.BlocksGeneratorConfig{
		NumChains:            numChains,
		MinBlockTimeInterval: 0,
		MaxBlockTimeInterval: 500 * time.Millisecond,
	}, nil, hashBlock)
	db, err := blockdb.NewMemBackedBlockDB()
	req.NoError(err)
	err = gen.Generate(0, genesisTime, genesisTime.Add(20*time.Second), db)
	req.NoError(err)
	iter, err := db.GetAll()
	req.NoError(err)

	revealer, err := test.NewRandomDAGRevealer(iter)
	req.NoError(err)

	genesisConfig := &totalOrderingConfig{
		roundBasedConfig: roundBasedConfig{
			roundInterval: 1000 * time.Second,
		},
		k:         0,
		phi:       uint64(numChains * 2 / 3),
		numChains: numChains,
	}
	genesisConfig.setRoundBeginTime(genesisTime)
	to1 := newTotalOrdering(genesisConfig)
	s.Require().NoError(to1.appendConfig(1, &types.Config{
		K:         0,
		PhiRatio:  0.5,
		NumChains: numChains,
	}))
	deliveredBlocks1 := [][]*types.Block{}
	for {
		b, err := revealer.Next()
		if err != nil {
			if err == blockdb.ErrIterationFinished {
				err = nil
				break
			}
		}
		s.Require().NoError(err)
		bs, _, err := to1.processBlock(&b)
		s.Require().Nil(err)
		if len(bs) > 0 {
			deliveredBlocks1 = append(deliveredBlocks1, bs)
		}
	}
	// Run new total ordering again.
	offset := len(deliveredBlocks1) / 2
	to2 := newTotalOrdering(genesisConfig)
	s.Require().NoError(to2.appendConfig(1, &types.Config{
		K:         0,
		PhiRatio:  0.5,
		NumChains: numChains,
	}))
	deliveredBlocks2 := [][]*types.Block{}
	for i := offset; i < len(deliveredBlocks1); i++ {
		for _, b := range deliveredBlocks1[i] {
			bs, _, err := to2.processBlock(b)
			req.NoError(err)
			if len(bs) > 0 {
				deliveredBlocks2 = append(deliveredBlocks2, bs)
			}
		}
	}
	// Check deliver1 and deliver2.
	for i := 0; i < len(deliveredBlocks2); i++ {
		req.Equal(len(deliveredBlocks1[offset+i]), len(deliveredBlocks2[i]))
		for j := 0; j < len(deliveredBlocks2[i]); j++ {
			req.Equal(deliveredBlocks1[offset+i][j], deliveredBlocks2[i][j])
		}
	}
}

func TestTotalOrdering(t *testing.T) {
	suite.Run(t, new(TotalOrderingTestSuite))
}
