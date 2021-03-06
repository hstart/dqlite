// Copyright 2017 Canonical Ltd.
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

package registry

import (
	"fmt"

	"github.com/CanonicalLtd/dqlite/internal/transaction"
	"github.com/CanonicalLtd/go-sqlite3"
)

// TxnLeaderAdd adds a new transaction to the registry.
//
// The given connection is assumed to be in leader replication mode.
func (r *Registry) TxnLeaderAdd(conn *sqlite3.SQLiteConn, id uint64) *transaction.Txn {
	// Check that no other leader connection is registered for the same
	// filename.
	filename := r.ConnLeaderFilename(conn)
	for other := range r.leaders {
		if other != conn && r.leaders[other] == filename {
			if txn := r.TxnByConn(other); txn != nil {
				serial := r.ConnSerial(other)
				panic(fmt.Sprintf("transaction %s registered on connection %d", txn, serial))
			}
		}
	}
	return r.txnAdd(conn, id, true)
}

// TxnLeaderByFilename returns the leader transaction associated with the given
// database filename, if any.
//
// If there is more than one leader transaction for the same filename, this
// method panics.
func (r *Registry) TxnLeaderByFilename(filename string) *transaction.Txn {
	var found *transaction.Txn
	for _, txn := range r.txns {
		if r.leaders[txn.Conn()] == filename {
			if found != nil {
				panic("found more than one leader transaction for this database")
			}
			found = txn
		}
	}
	return found
}

// TxnFollowerAdd adds a new follower transaction to the registry.
//
// The given connection is assumed to be in follower replication mode. The new
// transaction will be associated with the given transaction ID, which should
// match the one of the leader transaction that initiated the write.
func (r *Registry) TxnFollowerAdd(conn *sqlite3.SQLiteConn, id uint64) *transaction.Txn {
	return r.txnAdd(conn, id, false)
}

// TxnFollowerSurrogate creates a surrogate follower transaction.
//
// Surrogate follower transactions are used to replace leader transactions when
// a node loses leadership.
func (r *Registry) TxnFollowerSurrogate(txn *transaction.Txn) *transaction.Txn {
	if !txn.IsLeader() {
		panic("expected leader transaction")
	}
	r.TxnDel(txn.ID())
	filename := r.ConnLeaderFilename(txn.Conn())
	conn := r.ConnFollower(filename)
	txn = r.TxnFollowerAdd(conn, txn.ID())

	return txn
}

// TxnDel deletes the transaction with the given ID.
func (r *Registry) TxnDel(id uint64) {
	if _, ok := r.txns[id]; !ok {
		panic(fmt.Sprintf("attempt to remove unregistered transaction %d", id))
	}

	delete(r.txns, id)
}

// TxnByID returns the transaction with the given ID, if it exists.
func (r *Registry) TxnByID(id uint64) *transaction.Txn {
	txn, _ := r.txns[id]
	return txn
}

// TxnByConn returns the transaction associated with the given connection, if
// any.
func (r *Registry) TxnByConn(conn *sqlite3.SQLiteConn) *transaction.Txn {
	for _, txn := range r.txns {
		if txn.Conn() == conn {
			return txn
		}
	}
	return nil
}

// TxnByFilename returns the transaction associated with the given database
// filename, if any.
//
// If there is more than one transaction for the same filename, this method
// panics.
func (r *Registry) TxnByFilename(filename string) *transaction.Txn {
	conns := make([]*sqlite3.SQLiteConn, 0)

	if conn, ok := r.followers[filename]; ok {
		conns = append(conns, conn)
	}

	for conn := range r.leaders {
		if r.leaders[conn] == filename {
			conns = append(conns, conn)
		}
	}

	var found *transaction.Txn
	for _, conn := range conns {
		if txn := r.TxnByConn(conn); txn != nil {
			if found == nil {
				found = txn
			} else {
				panic("found more than one transaction for this database")
			}
		}
	}

	return found
}

// TxnDryRun makes transactions only transition between states, without
// actually invoking the relevant SQLite APIs. This should only be
// used by tests.
func (r *Registry) TxnDryRun() {
	r.txnDryRun = true
}

func (r *Registry) txnAdd(conn *sqlite3.SQLiteConn, id uint64, isLeader bool) *transaction.Txn {
	// Sanity check that a transaction for the same connection hasn't been
	// registered already. Iterating is fast since there will always be few
	// write transactions active at given time.
	if txn := r.TxnByConn(conn); txn != nil {
		panic(fmt.Sprintf(
			"a transaction for this connection is already registered with ID %d", txn.ID()))
	}

	txn := transaction.New(conn, id, isLeader, r.txnDryRun)
	r.txns[id] = txn
	return txn
}
