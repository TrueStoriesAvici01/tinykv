// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package raft

import (
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strconv"
	"time"

	pb "github.com/pingcap-incubator/tinykv/proto/pkg/eraftpb"
)

// None is a placeholder node ID used when there is no leader.
const None uint64 = 0

// StateType represents the role of a node in a cluster.
type StateType uint64

const (
	StateFollower StateType = iota
	StateCandidate
	StateLeader
)

var stmap = [...]string{
	"StateFollower",
	"StateCandidate",
	"StateLeader",
}

func (st StateType) String() string {
	return stmap[uint64(st)]
}

var (
	DTAG = " | [DEBUG] | raft : |"
	ETAG = " | [ERROR] | raft : |"
	FTAG = " | [FAILED] | raft : |"
)

// ErrProposalDropped is returned when the proposal is ignored by some cases,
// so that the proposer can be notified and fail fast.
var ErrProposalDropped = errors.New("raft proposal dropped")

// Config contains the parameters to start a raft.
type Config struct {
	// ID is the identity of the local raft. ID cannot be 0.
	ID uint64

	// peers contains the IDs of all nodes (including self) in the raft cluster. It
	// should only be set when starting a new raft cluster. Restarting raft from
	// previous configuration will panic if peers is set. peer is private and only
	// used for testing right now.
	peers []uint64

	// ElectionTick is the number of Node.Tick invocations that must pass between
	// elections. That is, if a follower does not receive any message from the
	// leader of current term before ElectionTick has elapsed, it will become
	// candidate and start an election. ElectionTick must be greater than
	// HeartbeatTick. We suggest ElectionTick = 10 * HeartbeatTick to avoid
	// unnecessary leader switching.
	ElectionTick int
	// HeartbeatTick is the number of Node.Tick invocations that must pass between
	// heartbeats. That is, a leader sends heartbeat messages to maintain its
	// leadership every HeartbeatTick ticks.
	HeartbeatTick int

	// Storage is the storage for raft. raft generates entries and states to be
	// stored in storage. raft reads the persisted entries and states out of
	// Storage when it needs. raft reads out the previous state and configuration
	// out of storage when restarting.
	Storage Storage
	// Applied is the last applied index. It should only be set when restarting
	// raft. raft will not return entries to the application smaller or equal to
	// Applied. If Applied is unset when restarting, raft might return previous
	// applied entries. This is a very application dependent configuration.
	Applied uint64
}

func (c *Config) validate() error {
	if c.ID == None {
		return errors.New("cannot use none as id")
	}

	if c.HeartbeatTick <= 0 {
		return errors.New("heartbeat tick must be greater than 0")
	}

	if c.ElectionTick <= c.HeartbeatTick {
		return errors.New("election tick must be greater than heartbeat tick")
	}

	if c.Storage == nil {
		return errors.New("storage cannot be nil")
	}

	return nil
}

// Progress represents a follower’s progress in the view of the leader. Leader maintains
// progresses of all followers, and sends entries to the follower based on its progress.
type Progress struct {
	Match, Next uint64
}

type Raft struct {
	id uint64

	Term uint64
	Vote uint64

	// the log
	RaftLog *RaftLog

	// log replication progress of each peers
	Prs map[uint64]*Progress

	// this peer's role
	State StateType

	// votes records
	votes map[uint64]bool

	voteCount uint64

	// msgs need to send
	msgs []pb.Message

	// the leader id
	Lead uint64

	// heartbeat interval, should send
	heartbeatTimeout int
	// baseline of election interval
	electionTimeout int
	// number of ticks since it reached last heartbeatTimeout.
	// only leader keeps heartbeatElapsed.
	heartbeatElapsed int
	// Ticks since it reached last electionTimeout when it is leader or candidate.
	// Number of ticks since it reached last electionTimeout or received a
	// valid message from current leader when it is a follower.
	electionElapsed int

	// leadTransferee is id of the leader transfer target when its value is not zero.
	// Follow the procedure defined in section 3.10 of Raft phd thesis.
	// (https://web.stanford.edu/~ouster/cgi-bin/papers/OngaroPhD.pdf)
	// (Used in 3A leader transfer)
	leadTransferee uint64

	// Only one conf change may be pending (in the log, but not yet
	// applied) at a time. This is enforced via PendingConfIndex, which
	// is set to a value >= the log index of the latest pending
	// configuration change (if any). Config changes are only allowed to
	// be proposed if the leader's applied index is greater than this
	// value.
	// (Used in 3A conf change)
	PendingConfIndex uint64
}

func (r Raft) String() string {
	return fmt.Sprintf("Raft: {id: %v | term: %v | vote for: %v | state: %v | leader: %v | heartbeat count: %v | election count: %v}", r.id, r.Term, r.Vote, r.State, r.Lead, r.heartbeatElapsed, r.electionElapsed)
}

// newRaft return a raft peer with the given config
func newRaft(c *Config) *Raft {
	if err := c.validate(); err != nil {
		panic(err.Error())
	}
	// Your Code Here (2A).
	rl := RaftLog{
		storage: c.Storage,
		entries: make([]pb.Entry, 0),
	}
	raft := Raft{
		id:               c.ID,
		RaftLog:          &rl,
		Prs:              make(map[uint64]*Progress),
		State:            StateFollower,
		votes:            make(map[uint64]bool),
		msgs:             make([]pb.Message, 0),
		electionTimeout:  c.ElectionTick,
		heartbeatTimeout: c.HeartbeatTick,
	}
	for _, id := range c.peers {
		prs := Progress{}
		raft.Prs[id] = &prs
	}
	log.Println(DTAG, " [new raft] |", raft)
	return &raft
}

// sendAppend sends an append RPC with new entries (if any) and the
// current commit index to the given peer. Returns true if a message was sent.
func (r *Raft) sendAppend(to uint64) bool {
	// Your Code Here (2A).
	msg := pb.Message{
		MsgType: pb.MessageType_MsgAppend,
		To:      to,
		From:    r.id,
		Term:    r.Term,
		Entries: make([]*pb.Entry, 0),
		Commit:  r.RaftLog.committed,
	}
	if p, ok := r.Prs[to]; ok {
		m := p.Match // TODO: what index we should use to as prevLogIndex
		if m >= uint64(len(r.RaftLog.entries)) {
			panic("opps~, find a illegal peer match index, peer id:" + strconv.Itoa(int(to)) + " match index: " + strconv.Itoa(int(m)))
		}
		msg.Index = r.RaftLog.entries[m].Index
		msg.LogTerm = r.RaftLog.entries[m].Term
		for i := m + 1; i < uint64(len(r.RaftLog.entries)); i++ {
			msg.Entries = append(msg.Entries, &r.RaftLog.entries[i])
		}
	} else {
		log.Println(DTAG, "not found peer progress. peer id:", to)
	}
	r.send(msg)
	log.Println(DTAG, " [send append] to:", to, "| raft:", r)
	return true
}

// sendHeartbeat sends a heartbeat RPC to the given peer.
func (r *Raft) sendHeartbeat(to uint64) {
	// Your Code Here (2A).
	msg := pb.Message{
		MsgType: pb.MessageType_MsgHeartbeat,
		To:      to,
		From:    r.id,
		Term:    r.Term,
		Commit:  r.RaftLog.committed,
	}
	if p, ok := r.Prs[to]; ok {
		m := p.Match
		if m >= uint64(len(r.RaftLog.entries)) {
			panic("opps~, find a illegal peer match index, peer id:" + strconv.Itoa(int(to)) + " match index: " + strconv.Itoa(int(m)))
		}
		msg.Index = r.RaftLog.entries[m].Index
		msg.LogTerm = r.RaftLog.entries[m].Term
	} else {
		log.Println(DTAG, "not found peer progress. peer id:", to)
	}
	r.send(msg)
	log.Println(DTAG, " [send heart beat] to:", to, "| raft:", r)
}

func (r *Raft) sendRequestVote(to uint64) {
	msg := pb.Message{
		MsgType: pb.MessageType_MsgRequestVote,
		To:      to,
		From:    r.id,
		Term:    r.Term,
	}
	if l := len(r.RaftLog.entries); l > 0 {
		msg.LogTerm = r.RaftLog.entries[l-1].Term
		msg.Index = r.RaftLog.entries[l-1].Index
	} else {
		log.Println(DTAG, "find no entry in node id:", r.id)
	}
	r.send(msg)
	log.Println(DTAG, "[send request vote] to:", to, "| raft:", r)
}

func (r *Raft) send(msg pb.Message) bool {
	log.Println(DTAG, " [send] | receive a message from:", msg.From, "| to:", msg.To, "| type:", msg.MsgType)
	r.msgs = append(r.msgs, msg)
	return true
}

// tick advances the internal logical clock by a single tick.
func (r *Raft) tick() {
	// Your Code Here (2A).
	log.Println(DTAG, "[tick] | raft:", r)
	switch r.State {
	case StateFollower:
		r.electionElapsed++
		if r.electionElapsed >= r.electionTimeout {
			r.startElection()
		}
	case StateCandidate:
	case StateLeader:
	}
}

// start a new election
//
// step:
//
// 1. increment current term by 1
//
// 2. transfer to candidate
//
// 3. vote for self
//
// 4. send RequestVote RPC to OTHER peers in parallel
//
// 5. reset election timeout and waiting for response from peer
func (r *Raft) startElection() {
	r.Term++
	r.State = StateCandidate
	r.Vote = r.id
	r.votes[r.Term] = true
	r.electionTimeout = int(100 + rand.Int31n(200))
	msg := pb.Message{
		MsgType: pb.MessageType_MsgHup,
		To:      r.id,
		From:    r.id,
		Term:    r.Term,
	}
	r.Step(msg)
	for id := range r.Prs {
		if id != r.id {
			r.sendRequestVote(id)
		}
	}
	log.Println(DTAG, "[start election] | election timeout:", r.electionTimeout, "| raft:", r)
}

func (r *Raft) broadcastHeartbeat() {
	for id := range r.Prs {
		if id != r.id {
			r.sendHeartbeat(id)
		}
	}
	log.Println(DTAG, "[broadcast heartbeat] | raft:", r)
}

func (r *Raft) broadcastAppend() {
	for id := range r.Prs {
		if id != r.id {
			r.sendAppend(id)
		}
	}
}

// becomeFollower transform this peer's state to Follower
//
// step:
//
// 1. update current term
//
// 2. update current leader id
//
// 3. update peer state
//
// 4. set heartbeat count as 0
//
// 5. set election count as 0
//
func (r *Raft) becomeFollower(term uint64, lead uint64) {
	// Your Code Here (2A).
	r.Term = term
	r.Lead = lead
	r.State = StateFollower
	r.heartbeatElapsed = 0
	r.electionElapsed = 0
	log.Println(DTAG, "[become follower] | raft:", r)
}

// becomeCandidate transform this peer's state to candidate
//
// step:
//
// 1. increment current term by 1
//
// 2. transfer to candidate
//
// 3. vote for self
//
// 4. increment granted vote count by 1, win if granted vote count no less than half of cluster
//
// 4. send RequestVote RPC to peers in parallel
//
// 5. reset election timeout and waiting for response
//
// 6. set electionelapsed as 0
//
func (r *Raft) becomeCandidate() {
	// Your Code Here (2A).
	r.Term++
	r.State = StateCandidate
	r.Vote = r.id
	r.votes[r.Term] = true
	r.voteCount = 1
	r.heartbeatElapsed = 0
	r.electionElapsed = 0
	rand.Seed(time.Now().UnixNano())
	r.electionTimeout = 5 + rand.Int()%6
	for id := range r.Prs {
		if id != r.id {
			r.sendRequestVote(id)
		}
	}
	if r.voteCount >= uint64(math.Floor(float64(len(r.Prs)/2)+0.5)) {
		msg := pb.Message{
			MsgType: pb.MessageType_MsgTransferLeader,
			To:      r.id,
			From:    r.id,
			Term:    r.Term,
		}
		r.send(msg)
	}
	log.Println(DTAG, "[become condidate] | raft:", r)
}

// becomeLeader transform this peer's state to leader
//
// step:
//
// 1. transfer to leader
//
// 2. update leader id as self
//
// 3. init peers progress ==> next index: leader last log index + 1; match index ==> index of highest log entry known to be replicated on server
//
// 4. set heartbeat count as 0
//
// 5. set election count as 0
//
// 6. send heartbeat(AppendEntries RPC) to peers
func (r *Raft) becomeLeader() {
	// Your Code Here (2A).
	// NOTE: Leader should propose a noop entry on its term
	r.State = StateLeader
	r.Lead = r.id
	r.heartbeatElapsed = 0
	r.electionElapsed = 0
	for id := range r.Prs {
		if l := len(r.RaftLog.entries); l > 0 {
			r.Prs[id].Next = r.RaftLog.entries[l].Index + 1
		}
		if id != r.id {
			r.sendHeartbeat(id)
		}
	}
	// TODO: propose a noop entry
	noop := pb.Message{
		MsgType: pb.MessageType_MsgHup,
		To: r.id,
		From: r.id,
		Term: r.Term,
	}
	r.send(noop)
	log.Println(DTAG, "[become leader] |", r)
}

// Step the entrance of handle message, see `MessageType`
// on `eraftpb.proto` for what msgs should be handled
func (r *Raft) Step(m pb.Message) error {
	// Your Code Here (2A).
	log.Println(DTAG, "[step] | message:", m, "|", r)

	switch r.State {
	case StateFollower:
		r.stepFollower(m)
	case StateCandidate:
		r.stepCandidate(m)
	case StateLeader:
		r.stepLeader(m)
	}
	return nil
}

func (r *Raft) stepFollower(m pb.Message) error {
	log.Println(DTAG, "[step follower] | Message:", m, "|", r)
	defer log.Println(DTAG, "[after step follower] |", r)
	switch m.MsgType {
	case pb.MessageType_MsgPropose: // redirect to leader
		if _, ok := r.Prs[r.Lead]; ok {
			m.To = r.Lead
			r.send(m)
		} else { // no leader now, reject this propose
			log.Println(DTAG, "try to redirect propose to leader, but no leader found. from:", m.From)
			return errors.New("no leader found, reject this propose")
		}
	case pb.MessageType_MsgAppend:
		r.handleAppendEntries(m)
	case pb.MessageType_MsgHeartbeat:
		r.electionElapsed = 0
		r.Lead = m.From
		r.handleHeartbeat(m)
	case pb.MessageType_MsgSnapshot:
		r.electionElapsed = 0
		r.Lead = m.From
		r.handleSnapshot(m)
	case pb.MessageType_MsgTransferLeader:
		m.To = r.Lead
		r.send(m)
	}
	return nil
}

func (r *Raft) stepCandidate(m pb.Message) {
	log.Println(DTAG, "[step candidate] | Message:", m)
	defer log.Println(DTAG, "[step candidate] after | Raft:", r)

	switch m.MsgType {
	case pb.MessageType_MsgPropose:
		log.Println(DTAG, "current candidate, receive a propose ==> drop it")
	case pb.MessageType_MsgAppend:
		r.becomeFollower(m.Term, m.From)
		r.handleAppendEntries(m)
	case pb.MessageType_MsgHeartbeat:
		r.becomeFollower(m.Term, m.From)
		r.handleHeartbeat(m)
	case pb.MessageType_MsgSnapshot:
		r.becomeFollower(m.Term, m.From)
		r.handleSnapshot(m)
	case pb.MessageType_MsgRequestVoteResponse:
		if m.To == r.id && !m.Reject {
			r.voteCount++
		}
		if int(r.voteCount) >= len(r.Prs) {
			r.becomeLeader()
		}
	}
}

func (r *Raft) stepLeader(m pb.Message) {
	log.Println(DTAG, "[step leader] | Message:", m)
	defer log.Println(DTAG, "[step leader] after | Raft:", r)

	switch m.MsgType {
	case pb.MessageType_MsgBeat:
		r.broadcastHeartbeat()
	case pb.MessageType_MsgPropose:
		r.broadcastAppend()
	case pb.MessageType_MsgAppendResponse:
		r.handleAppendResponse(m)
	case pb.MessageType_MsgHeartbeatResponse:
		r.handleHeartbeatResponse(m)
	}
}

// handleAppendEntries handle AppendEntries RPC request
//
// step:
//
// 1. message term less than current term ==> reject
//
// 2. no entry with prevLogTerm in prevLogIndex of log ==> reject
//
// 3. there is a entry in log has same same index but different term with new entry ==> delete this entry and all subsequent
//
// 4. any entries that not exists in log ==> append
//
// 5. leader commit index > current commit index ==> set commit index = min(leader commit index, last new entry index)
func (r *Raft) handleAppendEntries(m pb.Message) {
	// Your Code Here (2A).
	log.Println(DTAG, "[handle append entries] | message:", m, "|", r)
	
	resp := pb.Message{
		MsgType: pb.MessageType_MsgAppendResponse,
		To:      m.From,
		From:    r.id,
		Term:    r.Term,
		Commit:  r.RaftLog.committed,
	}
	defer log.Println(DTAG, "[after handle append entries] | message:", m)
	defer r.ApplyEntries()

	if m.Term < r.Term {
		log.Println(DTAG, "find a outdated message. message term:", m.Term, ", current term:", r.Term)
		resp.Reject = true
		if l := len(r.RaftLog.entries); l > 0 {
			resp.Index = r.RaftLog.entries[l-1].Index
			resp.LogTerm = r.RaftLog.entries[l-1].Term
		}
		r.send(resp)
		return
	} else if m.Term > r.Term {
		log.Println(DTAG, "may find a leader, become follower. message term:", m.Term, ", current term:", r.Term)
		resp.Term = m.Term
		r.becomeFollower(m.Term, m.From)
	}

	if m.Index < 0 || m.Index >= uint64(len(r.RaftLog.entries)) || r.RaftLog.entries[m.Index].Term != m.LogTerm {
		log.Println(DTAG, "cannot find previous log with index:", m.Index, ", term:", m.Term, ", log length:", len(r.RaftLog.entries))
		if l := len(r.RaftLog.entries); l > 0 {
			resp.Index = r.RaftLog.entries[l-1].Index
			resp.LogTerm = r.RaftLog.entries[l-1].Term
		}
		resp.Reject = true
		r.send(resp)
		return
	}

	for _, e := range m.Entries {
		ei, et := e.Index, e.Term
		if ei >= 0 && ei < uint64(len(r.RaftLog.entries)) {
			tt := r.RaftLog.entries[ei].Term
			if tt != et { // conflict, same index but different term
				log.Println(DTAG, "log conflict. index:", ei, ", message term:", et, ", log entry term:", tt)
				r.RaftLog.entries = r.RaftLog.entries[:ei] // TODO: what if entry index = 0
			} else {
				log.Println(DTAG, "log entry match. index:", ei, ", term:", et)
			}
		} else if ei == uint64(len(r.RaftLog.entries)) {
			r.RaftLog.entries = append(r.RaftLog.entries, *e)
		} else { // new entry index exceed current log ==> reject
			log.Println(FTAG, "find a illegal entry. Entry index:", ei, "| current log length:", len(r.RaftLog.entries))
			if l := len(r.RaftLog.entries); l > 0 {
				resp.Index = r.RaftLog.entries[l - 1].Index
				resp.LogTerm = r.RaftLog.entries[l - 1].Term
			}
			resp.Reject = true
			r.send(resp)
			return
		}
	}

	if m.Commit > r.RaftLog.committed {
		log.Println(DTAG, "leader commit index != my commit index: leader commit index:", m.Commit, "| my commit index:", r.RaftLog.committed)
		r.RaftLog.committed = min(m.Commit, r.RaftLog.entries[len(r.RaftLog.entries) - 1].Index)
	} else {
		log.Println(DTAG, "leader commit index:", m.Commit, "| my commit index:", r.RaftLog.committed)
	}

}

// for all servers, if commit index > last applied index, apply log[lastApplied] to state machine
func (r *Raft) ApplyEntries() {
	for r.RaftLog.committed > r.RaftLog.applied {
		r.RaftLog.applied++
	}
}

func (r *Raft) handleAppendResponse(m pb.Message) {

}

// handleHeartbeat handle Heartbeat RPC request
func (r *Raft) handleHeartbeat(m pb.Message) {
	// Your Code Here (2A).
}

func (r *Raft) handleHeartbeatResponse(m pb.Message) {

}

// handleSnapshot handle Snapshot RPC request
func (r *Raft) handleSnapshot(m pb.Message) {
	// Your Code Here (2C).
}

// addNode add a new node to raft group
func (r *Raft) addNode(id uint64) {
	// Your Code Here (3A).
}

// removeNode remove a node from raft group
func (r *Raft) removeNode(id uint64) {
	// Your Code Here (3A).
}
