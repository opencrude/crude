package sessiondb

import (
	"bytes"
	"container/list"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"crude/agent/provider"
)

// Message lists: a context, and a recovery checkpoint's archive and active
// context. Supervised runs publish both every tool round; they hold the whole
// history (megabytes on a long session) and grow by one round at a time.
// Storing each as one JSON array made every round decode, validate, encode and
// write the whole history again.
//
// A list is a base JSON array in its owning row followed by tail rows, one
// encoded message per row in position order. The list's encoding is the base
// array extended with the tail rows, byte-identical to json.Marshal of the
// whole list. Writers keep the base empty ([], or null for a nil list) and
// every message in tail rows: the owning row also holds the context revision
// or recovery status that changes every round, and SQLite rewrites a whole row
// on any update. A write replaces tail rows from the first changed message; a
// list whose base holds messages is moved to tail rows by its next write.
//
// What is stored must be known to plan a write without reading it back. The
// context's stored list is the current context, cached by its revision and
// write time; a recovery list is remembered by its field's version. Both
// caches hold the messages a transaction committed, recorded after commit.
//
// Published messages are immutable values: no caller mutates a message's
// nested slices in place, so cached slices may share them. Slices leave a
// cache clipped or copied, so an append or element write never reaches it.

// messageCacheBytes bounds each cache by the approximate size of its messages.
const messageCacheBytes = 128 << 20

// storedList is a list and how many of its leading messages the base holds.
type storedList struct {
	messages []provider.ChatMessage
	base     int
}

func (l storedList) tail() int { return len(l.messages) - l.base }

// listWrite stores next over a stored list: tail rows from keep on are
// replaced by tail, and a rewrite also replaces the base with base.
type listWrite struct {
	rewrite bool
	base    []byte
	keep    int
	tail    [][]byte
	result  storedList
}

// changesTail reports whether the write touches the storedTail rows.
func (w listWrite) changesTail(storedTail int) bool {
	return w.rewrite || len(w.tail) != 0 || w.keep != storedTail
}

var (
	emptyListBase = []byte("[]")
	nilListBase   = []byte("null")
)

// planListWrite stores next over stored, whose first shared messages equal
// next's. An unknown stored list, or one with messages in its base, is
// rewritten whole.
func planListWrite(stored storedList, known bool, shared int, next []provider.ChatMessage) (listWrite, error) {
	// A nil list encodes as null, which tail rows cannot extend.
	write := listWrite{keep: shared, result: storedList{messages: next}}
	if !known || stored.base != 0 || (stored.messages == nil) != (next == nil) {
		write = listWrite{rewrite: true, base: emptyListBase, result: storedList{messages: next}}
		if next == nil {
			write.base = nilListBase
		}
		shared = 0
	}
	for i := shared; i < len(next); i++ {
		encoded, err := json.Marshal(&next[i])
		if err != nil {
			return listWrite{}, err
		}
		write.tail = append(write.tail, encoded)
	}
	return write, nil
}

// decodeMessageList decodes a base array and its tail rows.
func decodeMessageList(base []byte, tail [][]byte) (storedList, error) {
	var messages []provider.ChatMessage
	if err := json.Unmarshal(base, &messages); err != nil {
		return storedList{}, err
	}
	list := storedList{base: len(messages)}
	if len(tail) != 0 && messages == nil {
		return storedList{}, errors.New("sessiondb: message list tail extends a null base")
	}
	for _, raw := range tail {
		var message provider.ChatMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return storedList{}, err
		}
		messages = append(messages, message)
	}
	list.messages = messages
	return list, nil
}

// encodeMessageList is the whole list's encoding: base extended by tail.
func encodeMessageList(base []byte, tail [][]byte) []byte {
	if len(tail) == 0 {
		return base
	}
	size := len(base) + len(tail)
	for _, raw := range tail {
		size += len(raw)
	}
	encoded := append(make([]byte, 0, size), bytes.TrimSuffix(base, []byte("]"))...)
	for _, raw := range tail {
		if encoded[len(encoded)-1] != '[' {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, raw...)
	}
	return append(encoded, ']')
}

func queryTail(tx *sql.Tx, query string, args ...any) ([][]byte, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tail [][]byte
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		tail = append(tail, raw)
	}
	return tail, rows.Err()
}

// Context lists.

type contextCacheKey struct {
	db                 *sql.DB
	sessionID, agentID string
}

// contextCacheEntry is the committed context at exactly one revision and write
// time. The row's revision only advances, under a compare-and-set, so a row
// that still carries both holds exactly this list.
type contextCacheEntry struct {
	revision  uint64
	updatedAt int64
	list      storedList
}

var contextCache = newByteLRU[contextCacheKey, contextCacheEntry](messageCacheBytes)

// rememberContext caches a context whose messages array sessiondb owns: one
// it decoded or built, which it hands out only clipped or copied.
func rememberContext(key contextCacheKey, entry contextCacheEntry) {
	contextCache.put(key, entry, approxMessagesBytes(entry.list.messages))
}

func loadContextTailTx(tx *sql.Tx, key contextCacheKey) ([][]byte, error) {
	return queryTail(tx, `SELECT message_json FROM agent_context_tail WHERE session_id=? AND agent_id=? ORDER BY position`, key.sessionID, key.agentID)
}

// loadContextEncodingTx reads the current context's whole encoding.
func loadContextEncodingTx(tx *sql.Tx, key contextCacheKey) ([]byte, error) {
	var base []byte
	if err := tx.QueryRow(`SELECT messages_json FROM agent_context_state WHERE session_id=? AND agent_id=?`, key.sessionID, key.agentID).Scan(&base); err != nil {
		return nil, err
	}
	tail, err := loadContextTailTx(tx, key)
	if err != nil {
		return nil, err
	}
	return encodeMessageList(base, tail), nil
}

// writeContextTailTx applies a write's tail rows; the caller stores its base.
func writeContextTailTx(tx *sql.Tx, key contextCacheKey, stored storedList, write listWrite) error {
	if !write.changesTail(stored.tail()) {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM agent_context_tail WHERE session_id=? AND agent_id=? AND position>=?`, key.sessionID, key.agentID, write.keep); err != nil {
		return err
	}
	for i, raw := range write.tail {
		if _, err := tx.Exec(`INSERT INTO agent_context_tail(session_id,agent_id,position,message_json) VALUES(?,?,?,?)`, key.sessionID, key.agentID, write.keep+i, raw); err != nil {
			return err
		}
	}
	return nil
}

// Recovery lists.
//
// An incident's archive and active context are lists of its runtime agent
// (agent_message_lists), not of the incident. A turn starts a new incident
// whose archive is the whole history plus the new input; owning lists per
// incident wrote that history again on every turn (~1 s and ~70 MB for 2,000
// messages) and deleted it when the turn ended. An agent has one unfinished
// run at a time, so a new incident takes over the list its agent's previous
// run left and writes what it adds. A list another unfinished incident still
// holds is never taken: the new incident starts its own list instead.

const (
	recoveryArchiveField = "archive"
	recoveryActiveField  = "active"
)

// recoveryListKey identifies one list. List IDs are never reused, so an ID
// and version name exactly one stored content.
type recoveryListKey struct {
	db     *sql.DB
	listID int64
}

type recoveryListEntry struct {
	version int64
	list    storedList
}

var recoveryLists = newByteLRU[recoveryListKey, recoveryListEntry](messageCacheBytes)

// A publication can share the active array the context cache already owns.
// Standalone checkpoint lists and lists returned to callers must be copied.
func rememberRecoveryList(key recoveryListKey, entry recoveryListEntry, owned bool) {
	if !owned {
		entry.list.messages = append([]provider.ChatMessage(nil), entry.list.messages...)
	}
	recoveryLists.put(key, entry, approxMessagesBytes(entry.list.messages))
}

// recoveryListRef is the list and version that hold one field of an
// incident. A field without a list was never written as one. current reports
// that the list still stores that version.
type recoveryListRef struct {
	listID, version int64
	current         bool
}

// recoveryListRefsTx reads an incident's list references by field.
func recoveryListRefsTx(tx *sql.Tx, incidentID string) (map[string]recoveryListRef, error) {
	rows, err := tx.Query(`SELECT r.field,r.list_id,r.version,COALESCE(l.version,0) FROM agent_recovery_lists r
		LEFT JOIN agent_message_lists l ON l.list_id=r.list_id WHERE r.incident_id=?`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make(map[string]recoveryListRef, 2)
	for rows.Next() {
		var field string
		var ref recoveryListRef
		var stored int64
		if err := rows.Scan(&field, &ref.listID, &ref.version, &stored); err != nil {
			return nil, err
		}
		ref.current = stored == ref.version
		refs[field] = ref
	}
	return refs, rows.Err()
}

// recoveryListTarget is the list a checkpoint writes one field into, at the
// version it currently stores. A zero listID means a new list.
type recoveryListTarget struct {
	listID, version int64
}

// recoveryListTargetTx chooses where an incident's field is written: its own
// list while it still holds it, else the newest list of its agent that no
// other unfinished incident holds, else a new list.
func recoveryListTargetTx(tx *sql.Tx, incidentID, agentID, field string, own recoveryListRef) (recoveryListTarget, error) {
	if own.listID != 0 && own.current {
		return recoveryListTarget{listID: own.listID, version: own.version}, nil
	}
	var target recoveryListTarget
	err := tx.QueryRow(`SELECT list_id,version FROM agent_message_lists WHERE agent_id=? AND field=? ORDER BY list_id DESC LIMIT 1`, agentID, field).Scan(&target.listID, &target.version)
	if errors.Is(err, sql.ErrNoRows) {
		return recoveryListTarget{}, nil
	}
	if err != nil {
		return recoveryListTarget{}, err
	}
	var held bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agent_recovery_lists l JOIN agent_recoveries r ON r.incident_id=l.incident_id
		WHERE l.list_id=? AND l.version=? AND r.incident_id<>? AND r.state NOT IN (?,?,?,?))`,
		target.listID, target.version, incidentID, settledRecoveryStates[0], settledRecoveryStates[1], settledRecoveryStates[2], settledRecoveryStates[3]).Scan(&held); err != nil {
		return recoveryListTarget{}, err
	}
	if held {
		return recoveryListTarget{}, nil
	}
	return target, nil
}

// planRecoveryListTx plans storing messages into target and reports the list
// to write and how many rows it holds. The remembered list is used only while
// its version is the stored one. A stored list this process does not
// remember (the first turn after a restart) is compared with its rows.
func planRecoveryListTx(tx *sql.Tx, db *sql.DB, target recoveryListTarget, messages []provider.ChatMessage) (recoveryListTarget, int, listWrite, error) {
	var remembered recoveryListEntry
	known := false
	if target.listID != 0 {
		remembered, known = recoveryLists.get(recoveryListKey{db, target.listID})
		known = known && remembered.version == target.version
		if !known && messages != nil {
			return planStoredListWriteTx(tx, target, messages)
		}
	}
	shared := 0
	if known {
		shared = commonMessagePrefix(remembered.list.messages, messages)
	}
	write, err := planListWrite(remembered.list, known, shared, messages)
	return target, remembered.list.tail(), write, err
}

// planStoredListWriteTx plans storing next over list rows whose messages are
// not in memory. Rows are compared byte for byte with the encoded messages
// up to the first difference; rows that already hold them stay and only the
// rest is written. When fewer rows match than would be deleted, a new list
// is written instead: it inserts no more rows, and the old list's rows are
// removed when its run ends (DeleteRecoveryCheckpoint), not while a prompt
// is being sent. The write also empties the incident's base, which an older
// incident may still hold messages in, so the rows alone are the list.
func planStoredListWriteTx(tx *sql.Tx, target recoveryListTarget, next []provider.ChatMessage) (recoveryListTarget, int, listWrite, error) {
	fail := func(err error) (recoveryListTarget, int, listWrite, error) {
		return recoveryListTarget{}, 0, listWrite{}, err
	}
	var stored int
	if err := tx.QueryRow(`SELECT count(*) FROM agent_message_list_rows WHERE list_id=?`, target.listID).Scan(&stored); err != nil {
		return fail(err)
	}
	rows, err := tx.Query(`SELECT message_json FROM agent_message_list_rows WHERE list_id=? ORDER BY position`, target.listID)
	if err != nil {
		return fail(err)
	}
	encoded := make([][]byte, 0, len(next))
	keep := 0
	for keep < len(next) && rows.Next() {
		var raw sql.RawBytes
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return fail(err)
		}
		message, err := json.Marshal(&next[keep])
		if err != nil {
			rows.Close()
			return fail(err)
		}
		encoded = append(encoded, message)
		if !bytes.Equal(message, raw) {
			break
		}
		keep++
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	for i := len(encoded); i < len(next); i++ {
		message, err := json.Marshal(&next[i])
		if err != nil {
			return fail(err)
		}
		encoded = append(encoded, message)
	}
	write := listWrite{rewrite: true, base: emptyListBase, result: storedList{messages: next}}
	if keep < stored-keep {
		write.tail = encoded
		return recoveryListTarget{}, 0, write, nil
	}
	write.keep, write.tail = keep, encoded[keep:]
	return target, stored, write, nil
}

// writeRecoveryListTx applies a planned write's rows to target and records
// the incident's reference own moves to; the caller has stored the
// incident's base. An unchanged list keeps its version. The returned function
// records the written list once the transaction commits.
func writeRecoveryListTx(tx *sql.Tx, db *sql.DB, incidentID, agentID, field string, own recoveryListRef, target recoveryListTarget, storedRows int, write listWrite, owned bool) (func(), error) {
	listID, version := target.listID, target.version
	changed := listID == 0 || write.changesTail(storedRows)
	if changed {
		version++
		if listID == 0 {
			result, err := tx.Exec(`INSERT INTO agent_message_lists(agent_id,field,version) VALUES(?,?,?)`, agentID, field, version)
			if err != nil {
				return nil, err
			}
			if listID, err = result.LastInsertId(); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.Exec(`DELETE FROM agent_message_list_rows WHERE list_id=? AND position>=?`, listID, write.keep); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`UPDATE agent_message_lists SET agent_id=?,version=? WHERE list_id=?`, agentID, version, listID); err != nil {
				return nil, err
			}
		}
		for i, raw := range write.tail {
			if _, err := tx.Exec(`INSERT INTO agent_message_list_rows(list_id,position,message_json) VALUES(?,?,?)`, listID, write.keep+i, raw); err != nil {
				return nil, err
			}
		}
	}
	if !changed && own.listID == listID && own.version == version {
		return func() {}, nil
	}
	if _, err := tx.Exec(`INSERT INTO agent_recovery_lists(incident_id,field,list_id,version) VALUES(?,?,?,?)
		ON CONFLICT(incident_id,field) DO UPDATE SET list_id=excluded.list_id,version=excluded.version`, incidentID, field, listID, version); err != nil {
		return nil, err
	}
	if !changed {
		return func() {}, nil
	}
	key := recoveryListKey{db, listID}
	return func() { rememberRecoveryList(key, recoveryListEntry{version: version, list: write.result}, owned) }, nil
}

// errRecoveryListReplaced reports an incident whose list a later run of its
// agent rewrote. Only a settled incident can lose its list this way.
var errRecoveryListReplaced = errors.New("sessiondb: a later run replaced this incident's recovery list")

// loadRecoveryListTx decodes an incident field's base and list rows, and
// remembers the list at its stored version so the next checkpoint extends it.
func loadRecoveryListTx(tx *sql.Tx, db *sql.DB, ref recoveryListRef, field string, base []byte) ([]provider.ChatMessage, error) {
	var rows [][]byte
	if ref.listID != 0 {
		if !ref.current {
			return nil, fmt.Errorf("%w (%s)", errRecoveryListReplaced, field)
		}
		var err error
		if rows, err = queryTail(tx, `SELECT message_json FROM agent_message_list_rows WHERE list_id=? ORDER BY position`, ref.listID); err != nil {
			return nil, err
		}
	}
	stored, err := decodeMessageList(base, rows)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: decode recovery %s: %w", field, err)
	}
	if ref.listID != 0 {
		rememberRecoveryList(recoveryListKey{db, ref.listID}, recoveryListEntry{version: ref.version, list: stored}, false)
	}
	return stored.messages, nil
}

// sameMessageArray reports exact aliasing of a nonempty message slice.
func sameMessageArray(a, b []provider.ChatMessage) bool {
	return len(a) != 0 && len(a) == len(b) && &a[0] == &b[0]
}

// commonMessagePrefix is the number of leading messages a and b share.
func commonMessagePrefix(a, b []provider.ChatMessage) int {
	n := min(len(a), len(b))
	for i := range n {
		if !sameMessage(&a[i], &b[i]) {
			return i
		}
	}
	return n
}

// sameMessage is reflect.DeepEqual for messages. A republished message is
// usually a copy of the stored one, sharing its strings, slices and pointers;
// identical struct bytes prove it equal without walking its fields.
func sameMessage(a, b *provider.ChatMessage) bool {
	size := int(unsafe.Sizeof(*a))
	if bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(a)), size), unsafe.Slice((*byte)(unsafe.Pointer(b)), size)) {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// approxMessagesBytes estimates the memory messages retain, for cache bounds.
func approxMessagesBytes(messages []provider.ChatMessage) int {
	size := 0
	for i := range messages {
		message := &messages[i]
		size += 256 + len(message.Content) + len(message.ReasoningContent) + len(message.Reasoning) + len(message.DisplayContent)
		for _, call := range message.ToolCalls {
			size += len(call.Function.Arguments)
		}
		for _, detail := range message.ReasoningDetails {
			size += len(detail)
		}
	}
	return size
}

// byteLRU is a least-recently-used map bounded by the caller's size of each
// value. A value larger than the whole bound is not retained.
type byteLRU[K comparable, V any] struct {
	mu    sync.Mutex
	limit int
	size  int
	order *list.List
	items map[K]*list.Element
}

type byteLRUItem[K comparable, V any] struct {
	key   K
	value V
	size  int
}

func newByteLRU[K comparable, V any](limit int) *byteLRU[K, V] {
	return &byteLRU[K, V]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

func (c *byteLRU[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*byteLRUItem[K, V]).value, true
}

func (c *byteLRU[K, V]) put(key K, value V, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		c.size -= element.Value.(*byteLRUItem[K, V]).size
		c.order.Remove(element)
		delete(c.items, key)
	}
	if size > c.limit {
		return
	}
	c.items[key] = c.order.PushFront(&byteLRUItem[K, V]{key: key, value: value, size: size})
	c.size += size
	for c.size > c.limit {
		oldest := c.order.Back()
		item := oldest.Value.(*byteLRUItem[K, V])
		c.order.Remove(oldest)
		delete(c.items, item.key)
		c.size -= item.size
	}
}
