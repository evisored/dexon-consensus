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

package types

import (
	"container/heap"
	"encoding/binary"
	"math/big"

	"github.com/dexon-foundation/dexon-consensus-core/core/crypto"
)

// NodeSet is the node set structure as defined in DEXON consensus core.
type NodeSet struct {
	Nodes map[NodeID]struct{}
}

// SubSetTarget is the sub set target for GetSubSet().
type SubSetTarget *big.Int

type subSetTargetType byte

const (
	targetNotarySet subSetTargetType = iota
	targetWitnessSet
	targetDKGSet
)

type nodeRank struct {
	ID   NodeID
	rank *big.Int
}

// rankHeap is a MaxHeap structure.
type rankHeap []*nodeRank

func (h rankHeap) Len() int           { return len(h) }
func (h rankHeap) Less(i, j int) bool { return h[i].rank.Cmp(h[j].rank) > 0 }
func (h rankHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *rankHeap) Push(x interface{}) {
	*h = append(*h, x.(*nodeRank))
}
func (h *rankHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// NewNodeSet creates a new NodeSet instance.
func NewNodeSet() *NodeSet {
	return &NodeSet{
		Nodes: make(map[NodeID]struct{}),
	}
}

// NewNotarySetTarget is the target for getting Notary Set.
func NewNotarySetTarget(crs []byte, shardID, chainID uint32) SubSetTarget {
	binaryShardID := make([]byte, 4)
	binary.LittleEndian.PutUint32(binaryShardID, shardID)

	binaryChainID := make([]byte, 4)
	binary.LittleEndian.PutUint32(binaryChainID, chainID)

	return newTarget(targetNotarySet, crs, binaryShardID, binaryChainID)
}

// NewWitnessSetTarget  is the target for getting DKG Set.
func NewWitnessSetTarget(crs []byte, round uint64) SubSetTarget {
	binaryRound := make([]byte, 8)
	binary.LittleEndian.PutUint64(binaryRound, round)

	return newTarget(targetWitnessSet, crs, binaryRound)
}

// NewDKGSetTarget  is the target for getting DKG Set.
func NewDKGSetTarget(crs []byte, round uint64) SubSetTarget {
	binaryRound := make([]byte, 8)
	binary.LittleEndian.PutUint64(binaryRound, round)

	return newTarget(targetDKGSet, crs, binaryRound)
}

// GetSubSet returns the subset of given target.
func (ns *NodeSet) GetSubSet(size int, target SubSetTarget) NodeIDs {
	h := rankHeap{}
	idx := 0
	for nID := range ns.Nodes {
		if idx < size {
			h = append(h, newNodeRank(nID, target))
		} else if idx == size {
			heap.Init(&h)
		}
		if idx >= size {
			rank := newNodeRank(nID, target)
			if rank.rank.Cmp(h[0].rank) < 0 {
				h[0] = rank
				heap.Fix(&h, 0)
			}
		}
		idx++
	}

	nIDs := make(NodeIDs, 0, size)
	for _, rank := range h {
		nIDs = append(nIDs, rank.ID)
	}

	return nIDs
}

func newTarget(targetType subSetTargetType, data ...[]byte) SubSetTarget {
	data = append(data, []byte{byte(targetType)})
	h := crypto.Keccak256Hash(data...)
	num := big.NewInt(0)
	num.SetBytes(h[:])
	return SubSetTarget(num)
}

func newNodeRank(ID NodeID, target SubSetTarget) *nodeRank {
	num := big.NewInt(0)
	num.SetBytes(ID.Hash[:])
	num.Abs(num.Sub((*big.Int)(target), num))
	return &nodeRank{
		ID:   ID,
		rank: num,
	}
}