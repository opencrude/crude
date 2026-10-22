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

	"crude/provider"
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

// changesTail reports whether the write touches tail rows.
func (w listWrite) changesTail(stored storedList) bool {
	return w.rewrite || len(w.tail) != 0 || w.keep != stored.tail()
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
	if !write.changesTail(stored) {
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

const (
	recoveryArchiveField = "archive"
	recoveryActiveField  = "active"
)

type recoveryListKey struct {
	db                *sql.DB
	incidentID, field string
}

type recoveryListEntry struct {
	version int64
	list    storedList
}

var recoveryLists = newByteLRU[recoveryListKey, recoveryListEntry](messageCacheBytes)

// rememberRecoveryList keeps its own copy: a checkpoint's lists can be its
// caller's arrays.
func rememberRecoveryList(key recoveryListKey, entry recoveryListEntry) {
	entry.list.messages = append([]provider.ChatMessage(nil), entry.list.messages...)
	recoveryLists.put(key, entry, approxMessagesBytes(entry.list.messages))
}

// recoveryListVersionsTx reads an incident's list versions by field; a field
// without one has never been written as a list.
func recoveryListVersionsTx(tx *sql.Tx, incidentID string) (map[string]int64, error) {
	rows, err := tx.Query(`SELECT field,version FROM agent_recovery_lists WHERE incident_id=?`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make(map[string]int64, 2)
	for rows.Next() {
		var field string
		var version int64
		if err := rows.Scan(&field, &version); err != nil {
			return nil, err
		}
		versions[field] = version
	}
	return versions, rows.Err()
}

// planRecoveryListTx plans storing messages as an incident field's list at
// its stored version. The remembered list is used only while its version is
// the stored one.
func planRecoveryListTx(key recoveryListKey, version int64, messages []provider.ChatMessage) (storedList, listWrite, error) {
	remembered, known := recoveryLists.get(key)
	known = known && version != 0 && remembered.version == version
	shared := 0
	if known {
		shared = commonMessagePrefix(remembered.list.messages, messages)
	}
	write, err := planListWrite(remembered.list, known, shared, messages)
	return remembered.list, write, err
}

// writeRecoveryListTx applies a planned write's tail rows and advances the
// field's version; the caller has stored its base. An unchanged list keeps
// its version. The returned function records the written list once the
// transaction commits.
func writeRecoveryListTx(tx *sql.Tx, key recoveryListKey, stored storedList, version int64, write listWrite) (func(), error) {
	if !write.changesTail(stored) {
		return func() {}, nil
	}
	if _, err := tx.Exec(`DELETE FROM agent_recovery_tail WHERE incident_id=? AND field=? AND position>=?`, key.incidentID, key.field, write.keep); err != nil {
		return nil, err
	}
	for i, raw := range write.tail {
		if _, err := tx.Exec(`INSERT INTO agent_recovery_tail(incident_id,field,position,message_json) VALUES(?,?,?,?)`, key.incidentID, key.field, write.keep+i, raw); err != nil {
			return nil, err
		}
	}
	version++
	if _, err := tx.Exec(`INSERT INTO agent_recovery_lists(incident_id,field,version) VALUES(?,?,?) ON CONFLICT(incident_id,field) DO UPDATE SET version=excluded.version`, key.incidentID, key.field, version); err != nil {
		return nil, err
	}
	return func() { rememberRecoveryList(key, recoveryListEntry{version: version, list: write.result}) }, nil
}

// loadRecoveryListTx decodes an incident field's base and tail rows, and
// remembers the list at its stored version so the incident's next checkpoint
// extends it.
func loadRecoveryListTx(tx *sql.Tx, key recoveryListKey, version int64, base []byte) ([]provider.ChatMessage, error) {
	tail, err := queryTail(tx, `SELECT message_json FROM agent_recovery_tail WHERE incident_id=? AND field=? ORDER BY position`, key.incidentID, key.field)
	if err != nil {
		return nil, err
	}
	stored, err := decodeMessageList(base, tail)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: decode recovery %s: %w", key.field, err)
	}
	if version != 0 {
		rememberRecoveryList(key, recoveryListEntry{version: version, list: stored})
	}
	return stored.messages, nil
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
