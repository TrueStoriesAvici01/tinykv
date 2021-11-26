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

/*
Package raft sends and receives messages in the Protocol Buffer format
defined in the eraftpb package.

raft包发送和接收以protocol buffer格式的消息，该格式定义在proto中的eraftpb包中。

Raft is a protocol with which a cluster of nodes can maintain a replicated state machine.
The state machine is kept in sync through the use of a replicated log.
For more details on Raft, see "In Search of an Understandable Consensus Algorithm"
(https://ramcloud.stanford.edu/raft.pdf) by Diego Ongaro and John Ousterhout.

Raft是一种协议，用来规定一个节点集群中保存可复制的状态机。状态机通过使用可复制的log实现同步。

Usage

The primary object in raft is a Node. You either start a Node from scratch
using raft.StartNode or start a Node from some initial state using raft.RestartNode.

raft中主要的角色是节点。可以通过raft.StarNode或从设定的初始状态使用raft.RestartNode启动节点。

To start a node from scratch:

直接从配置中启动节点：

  storage := raft.NewMemoryStorage()
  c := &Config{
    ID:              0x01,
    ElectionTick:    10,
    HeartbeatTick:   1,
    Storage:         storage,
  }
  n := raft.StartNode(c, []raft.Peer{{ID: 0x02}, {ID: 0x03}})

To restart a node from previous state:

从之前的状态中重新启动节点：

  storage := raft.NewMemoryStorage()

  // recover the in-memory storage from persistent
  // snapshot, state and entries.
  storage.ApplySnapshot(snapshot)
  storage.SetHardState(state)
  storage.Append(entries)

  c := &Config{
    ID:              0x01,
    ElectionTick:    10,
    HeartbeatTick:   1,
    Storage:         storage,
    MaxInflightMsgs: 256,
  }

  // restart raft without peer information.
  // peer information is already included in the storage.
  n := raft.RestartNode(c)

  重新启动不需要设定其他节点信息，其他节点信息已经保存在Storage变量中。

Now that you are holding onto a Node you have a few responsibilities:

对于一个节点，其主要的工作有：

First, you must read from the Node.Ready() channel and process the updates
it contains. These steps may be performed in parallel, except as noted in step
2.

首先，必须通过Node.Ready()通道，并对节点进行更新。这些步骤可能并行执行。

1. Write HardState, Entries, and Snapshot to persistent storage if they are
not empty. Note that when writing an Entry with Index i, any
previously-persisted entries with Index >= i must be discarded.

1. 若HardState，Entries，Snapshot非空，将其写入到持久化存储中(storage)。注意：保存一个index为i的日志元素时，之前持久化的日志中所有index >= i的元素必须抛弃。

2. Send all Messages to the nodes named in the To field. It is important that
no messages be sent until the latest HardState has been persisted to disk,
and all Entries written by any previous Ready batch (Messages may be sent while
entries from the same batch are being persisted).

2. 将所有消息发送到变量To中列出的节点。注意需要等到最新的HardState持久化到磁盘中才发送消息，并且所有的日志条目由已经准备好的batch写入。

Note: Marshalling messages is not thread-safe; it is important that you
make sure that no new entries are persisted while marshalling.
The easiest way to achieve this is to serialize the messages directly inside
your main raft loop.

注意：对消息进行marshall操作并非线程安全的。确保在进行marshall操作时没有新的日志条目可以持久化。最简单的方式是在主循环中直接序列化消息。

3. Apply Snapshot (if any) and CommittedEntries to the state machine.
If any committed Entry has Type EntryType_EntryConfChange, call Node.ApplyConfChange()
to apply it to the node. The configuration change may be cancelled at this point
by setting the NodeId field to zero before calling ApplyConfChange
(but ApplyConfChange must be called one way or the other, and the decision to cancel
must be based solely on the state machine and not external information such as
the observed health of the node).

3. 应用快照和已提交的日志条目到状态机中。如果任何已提交的日志条目的类型为EntryType_EntryConfChange,调用Node.ApplyConfChange()将这个日志条目应用到该节点中。此时配置信息的改变需要被取消，可以通过在调用ApplyConfChange前设置NodeId值为0。ApplyConfChange必须通过一种或另一种方式调用，取消配置更改必须基于状态机而非外部信息如：节点的健康状态。

4. Call Node.Advance() to signal readiness for the next batch of updates.
This may be done at any time after step 1, although all updates must be processed
in the order they were returned by Ready.

4. 调用Node.Advance()表明已经准备好下次批量更新。这可以在第一步后的任何时间执行，虽然所有的更新需要按照Ready返回的顺序被处理。

Second, all persisted log entries must be made available via an
implementation of the Storage interface. The provided MemoryStorage
type can be used for this (if you repopulate its state upon a
restart), or you can supply your own disk-backed implementation.

二：所有持久化的日志条目必须通过Storage接口的实现提供可用性，提供的MemoryStorage提供这样的支持（如果你通过重新启动的方式重现其状态），也可以提供自己的实现。

Third, when you receive a message from another node, pass it to Node.Step:

	func recvRaftRPC(ctx context.Context, m eraftpb.Message) {
		n.Step(ctx, m)
	}

三：若你接收到其他节点的消息，将其传给Node.Step()

Finally, you need to call Node.Tick() at regular intervals (probably
via a time.Ticker). Raft has two important timeouts: heartbeat and the
election timeout. However, internally to the raft package time is
represented by an abstract "tick".

最后：你需要定期调用Node.Tick()（可能通过time.Ticker）。Raft有两个超时时间：heartbeat和election timeout(心跳超时时间和选举超时时间)。而Raft内部的时间表示为抽象的tick。

The total state machine handling loop will look something like this:

  for {
    select {
    case <-s.Ticker:
      n.Tick()
    case rd := <-s.Node.Ready():
      saveToStorage(rd.State, rd.Entries, rd.Snapshot)
      send(rd.Messages)
      if !raft.IsEmptySnap(rd.Snapshot) {
        processSnapshot(rd.Snapshot)
      }
      for _, entry := range rd.CommittedEntries {
        process(entry)
        if entry.Type == eraftpb.EntryType_EntryConfChange {
          var cc eraftpb.ConfChange
          cc.Unmarshal(entry.Data)
          s.Node.ApplyConfChange(cc)
        }
      }
      s.Node.Advance()
    case <-s.done:
      return
    }
  }

To propose changes to the state machine from your node take your application
data, serialize it into a byte slice and call:

为了表示获取应用数据并将更改应用到节点的状态机中，将其序列化成字节切片并调用：

	n.Propose(data)

If the proposal is committed, data will appear in committed entries with type
eraftpb.EntryType_EntryNormal. There is no guarantee that a proposed command will be
committed; you may have to re-propose after a timeout.

如果提议的已经提交，数据将出现在已提交的日志条目中，其类型为eraftpb.EntryType_EntryNormal。没有任何保证提议的命令将会提交，需要自行在超时之后进行重新提议。

To add or remove a node in a cluster, build ConfChange struct 'cc' and call:

若要添加或移除集群中的一个节点，构建一个ConfChange实例cc然后调用：

	n.ProposeConfChange(cc)

After config change is committed, some committed entry with type
eraftpb.EntryType_EntryConfChange will be returned. You must apply it to node through:

在配置更改信息提交之后，一些类型为eraftpb.EntryType_EntryConfChange的已提交的日志条目将会返回。你必须通过如下方式将其应用到节点中：

	var cc eraftpb.ConfChange
	cc.Unmarshal(data)
	n.ApplyConfChange(cc)

Note: An ID represents a unique node in a cluster for all time. A
given ID MUST be used only once even if the old node has been removed.
This means that for example IP addresses make poor node IDs since they
may be reused. Node IDs must be non-zero.

注：一个ID代表集群中的唯一节点。一个ID只能只能使用一次，即使旧的节点已经被移除。这意味着使用IP地址作为节点ID的想法并不恰当，因为IP可能会被重用。并且节点ID必须非零。

Implementation notes

This implementation is up to date with the final Raft thesis
(https://ramcloud.stanford.edu/~ongaro/thesis.pdf), although our
implementation of the membership change protocol differs somewhat from
that described in chapter 4. The key invariant that membership changes
happen one node at a time is preserved, but in our implementation the
membership change takes effect when its entry is applied, not when it
is added to the log (so the entry is committed under the old
membership instead of the new). This is equivalent in terms of safety,
since the old and new configurations are guaranteed to overlap.

本实现是根据Raft最新的论文。我们的实现在成员变动协议中与原文略有不同。主要的差异在于每次一个节点发生成员变动是允许的，而我们的实现中成员变动在其日志条目被应用后才生效，而非其被加入到日志中生效（日志条目基于旧的成员关系提交而非新的成员关系提交）。这在安全性上是等效的，因为新旧配置确保叠加。

To ensure that we do not attempt to commit two membership changes at
once by matching log positions (which would be unsafe since they
should have different quorum requirements), we simply disallow any
proposed membership change while any uncommitted change appears in
the leader's log.

为了确保我们不会一次通过匹配日志位置尝试提交两个成员变动（这种情况不安全，因为应该需要不同的裁定人数要求），我们简单地在leader的日志中出现未提交的改动时禁止提交成员变动。

This approach introduces a problem when you try to remove a member
from a two-member cluster: If one of the members dies before the
other one receives the commit of the confchange entry, then the member
cannot be removed any more since the cluster cannot make progress.
For this reason it is highly recommended to use three or more nodes in
every cluster.

这种方式存在一个问题：在一个有两个成员的集群中，当移除一个成员时会发生异常：当一个成员在另一个成员接收到提交的confchange日志条目前宕机，则这个成员无法被移除，因为整个集群无法继续进行下去。为此，推荐一个集群中使用三个或更多的节点。

MessageType

Package raft sends and receives message in Protocol Buffer format (defined
in eraftpb package). Each state (follower, candidate, leader) implements its
own 'step' method ('stepFollower', 'stepCandidate', 'stepLeader') when
advancing with the given eraftpb.Message. Each step is determined by its
eraftpb.MessageType. Note that every step is checked by one common method
'Step' that safety-checks the terms of node and incoming message to prevent
stale log entries:

raft包发送和接收protocol buffer格式的消息（定义在eraftpb包中）。在给定eraftpb.Message时，每种状态（follower, candidata, leader）都实现自己的step方法（stepFollower, stepCandidate, stepLeader）。每个step方法是由其eraftpb.MessageType决定的。注意每一步是由一个通用的Step方法安全检查节点的term和收到的消息避免稳定的日志条目：

	'MessageType_MsgHup' is used for election. If a node is a follower or candidate, the
	'tick' function in 'raft' struct is set as 'tickElection'. If a follower or
	candidate has not received any heartbeat before the election timeout, it
	passes 'MessageType_MsgHup' to its Step method and becomes (or remains) a candidate to
	start a new election.

	MessageType_msgHup用于选举。当一个节点是follower或candidate，raft中的tick函数设置为tickEleection。如果follower或candidate在选举超时前没有收到任何心跳报文，他将会将MessageType_MsgHup传递给其Step方法并成为一个candiate开始新的选举。

	'MessageType_MsgBeat' is an internal type that signals the leader to send a heartbeat of
	the 'MessageType_MsgHeartbeat' type. If a node is a leader, the 'tick' function in
	the 'raft' struct is set as 'tickHeartbeat', and triggers the leader to
	send periodic 'MessageType_MsgHeartbeat' messages to its followers.

	MessageType_MsgBeat是一个内部状态，用来标记一个leader需要发送一个MessageType_MsgHeartbeat类型的心跳报文。如果节点是leader，则raft结构体中的tick函数被设置为tickHeartbeat，并且触发leader周期性的发送MessageType_MsgHeartbeat报文给其他的follower。

	'MessageType_MsgPropose' proposes to append data to its log entries. This is a special
	type to redirect proposals to the leader. Therefore, send method overwrites
	eraftpb.Message's term with its HardState's term to avoid attaching its
	local term to 'MessageType_MsgPropose'. When 'MessageType_MsgPropose' is passed to the leader's 'Step'
	method, the leader first calls the 'appendEntry' method to append entries
	to its log, and then calls 'bcastAppend' method to send those entries to
	its peers. When passed to candidate, 'MessageType_MsgPropose' is dropped. When passed to
	follower, 'MessageType_MsgPropose' is stored in follower's mailbox(msgs) by the send
	method. It is stored with sender's ID and later forwarded to the leader by
	rafthttp package.

	MessageType_MsgPropose为提议在日志条目中追加数据。这是将提议重定向到leader的特殊类型。

	'MessageType_MsgAppend' contains log entries to replicate. A leader calls bcastAppend,
	which calls sendAppend, which sends soon-to-be-replicated logs in 'MessageType_MsgAppend'
	type. When 'MessageType_MsgAppend' is passed to candidate's Step method, candidate reverts
	back to follower, because it indicates that there is a valid leader sending
	'MessageType_MsgAppend' messages. Candidate and follower respond to this message in
	'MessageType_MsgAppendResponse' type.

	'MessageType_MsgAppendResponse' is response to log replication request('MessageType_MsgAppend'). When
	'MessageType_MsgAppend' is passed to candidate or follower's Step method, it responds by
	calling 'handleAppendEntries' method, which sends 'MessageType_MsgAppendResponse' to raft
	mailbox.

	'MessageType_MsgRequestVote' requests votes for election. When a node is a follower or
	candidate and 'MessageType_MsgHup' is passed to its Step method, then the node calls
	'campaign' method to campaign itself to become a leader. Once 'campaign'
	method is called, the node becomes candidate and sends 'MessageType_MsgRequestVote' to peers
	in cluster to request votes. When passed to the leader or candidate's Step
	method and the message's Term is lower than leader's or candidate's,
	'MessageType_MsgRequestVote' will be rejected ('MessageType_MsgRequestVoteResponse' is returned with Reject true).
	If leader or candidate receives 'MessageType_MsgRequestVote' with higher term, it will revert
	back to follower. When 'MessageType_MsgRequestVote' is passed to follower, it votes for the
	sender only when sender's last term is greater than MessageType_MsgRequestVote's term or
	sender's last term is equal to MessageType_MsgRequestVote's term but sender's last committed
	index is greater than or equal to follower's.

	'MessageType_MsgRequestVoteResponse' contains responses from voting request. When 'MessageType_MsgRequestVoteResponse' is
	passed to candidate, the candidate calculates how many votes it has won. If
	it's more than majority (quorum), it becomes leader and calls 'bcastAppend'.
	If candidate receives majority of votes of denials, it reverts back to
	follower.

	'MessageType_MsgSnapshot' requests to install a snapshot message. When a node has just
	become a leader or the leader receives 'MessageType_MsgPropose' message, it calls
	'bcastAppend' method, which then calls 'sendAppend' method to each
	follower. In 'sendAppend', if a leader fails to get term or entries,
	the leader requests snapshot by sending 'MessageType_MsgSnapshot' type message.

	'MessageType_MsgHeartbeat' sends heartbeat from leader. When 'MessageType_MsgHeartbeat' is passed
	to candidate and message's term is higher than candidate's, the candidate
	reverts back to follower and updates its committed index from the one in
	this heartbeat. And it sends the message to its mailbox. When
	'MessageType_MsgHeartbeat' is passed to follower's Step method and message's term is
	higher than follower's, the follower updates its leaderID with the ID
	from the message.

	'MessageType_MsgHeartbeatResponse' is a response to 'MessageType_MsgHeartbeat'. When 'MessageType_MsgHeartbeatResponse'
	is passed to the leader's Step method, the leader knows which follower
	responded.

*/
package raft
