package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/1Barak/curate-mcp/pkg/graph"
)

var (
	ErrReceiptsNotConfigured  = errors.New("mcp: receipts path not configured")
	ErrReceiptsNotImplemented = errors.New("mcp: receipts operation not implemented")
)

// ReceiptEntry defines the canonical JSON schema compatible with hook_pre_mutation_guard.py.
type ReceiptEntry struct {
	TimestampEpoch  float64  `json:"timestamp_epoch"`
	TTLSeconds      int      `json:"ttl_seconds"`
	CodeSymbol      string   `json:"code.symbol"`
	CodeFilePath    string   `json:"code.filepath"`
	TraceID         string   `json:"trace_id"`
	InDegree        int      `json:"graph.in_degree"`
	OutDegree       int      `json:"graph.out_degree"`
	IC              int      `json:"graph.ic"`
	FTDEligible     bool     `json:"ftd.eligible"`
	UpstreamCallers []string `json:"upstream_callers"`
	AutoEmitted     bool     `json:"auto_emitted"`
}

// ReceiptEmitter coordinates thread-safe, atomic disk persistence of OTel receipts.
type ReceiptEmitter struct {
	mu           sync.Mutex
	receiptsPath string
}

// NewReceiptEmitter initializes a new emitter for the given receipts file path.
func NewReceiptEmitter(receiptsPath string) *ReceiptEmitter {
	return &ReceiptEmitter{
		receiptsPath: receiptsPath,
	}
}

// GetReceiptsPath returns the configured receipts file path.
func (r *ReceiptEmitter) GetReceiptsPath() string {
	return r.receiptsPath
}

// EmitReceipt writes an upserted receipt entry to disk with atomic rename and TTL purge.
func (r *ReceiptEmitter) EmitReceipt(entry ReceiptEntry) error {
	if r.receiptsPath == "" {
		return ErrReceiptsNotConfigured
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	existing := make(map[string]ReceiptEntry)
	if rawBytes, err := os.ReadFile(r.receiptsPath); err == nil && len(rawBytes) > 0 {
		_ = json.Unmarshal(rawBytes, &existing)
	}

	// Purge expired entries: now - timestamp_epoch > ttl_seconds
	nowEpoch := float64(time.Now().Unix())
	for k, v := range existing {
		if nowEpoch-v.TimestampEpoch > float64(v.TTLSeconds) {
			delete(existing, k)
		}
	}

	// Key is the normalized CodeFilePath
	key := entry.CodeFilePath
	if key == "" {
		key = entry.CodeSymbol
	}
	if entry.TTLSeconds <= 0 {
		entry.TTLSeconds = 900
	}
	if entry.TimestampEpoch <= 0 {
		entry.TimestampEpoch = nowEpoch
	}
	if entry.UpstreamCallers == nil {
		entry.UpstreamCallers = []string{}
	}

	existing[key] = entry

	// Prepare atomic write
	dir := filepath.Dir(r.receiptsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	rawBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}

	tmpUUID, _ := graph.GenerateUUIDv4()
	tmpFile := filepath.Join(dir, fmt.Sprintf(".tmp.%s", tmpUUID))

	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	if _, err := f.Write(rawBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpFile)
		return err
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpFile)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}

	// Atomic replace via os.Rename with 3 retries (2ms delay) for Windows lock mitigation
	var renameErr error
	for i := 0; i < 3; i++ {
		renameErr = os.Rename(tmpFile, r.receiptsPath)
		if renameErr == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if renameErr != nil {
		_ = os.Remove(tmpFile)
		return renameErr
	}

	return nil
}

// ReadReceipts reads the canonical receipts map from disk.
func (r *ReceiptEmitter) ReadReceipts() (map[string]ReceiptEntry, error) {
	if r.receiptsPath == "" {
		return nil, ErrReceiptsNotConfigured
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	rawBytes, err := os.ReadFile(r.receiptsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]ReceiptEntry), nil
		}
		return nil, err
	}

	if len(rawBytes) == 0 {
		return make(map[string]ReceiptEntry), nil
	}

	var receipts map[string]ReceiptEntry
	if err := json.Unmarshal(rawBytes, &receipts); err != nil {
		return nil, err
	}

	if receipts == nil {
		receipts = make(map[string]ReceiptEntry)
	}

	return receipts, nil
}
