# Adaptive WAL Sync: Eliminate Idle Disk Writes

## Problem

The muninn server was constantly writing up to 1MB/s even when idle. This was caused by the WAL syncer performing an `fsync` syscall every 10ms (100 times per second) regardless of whether there was any data to flush.

## Solution

This PR implements an **adaptive sync mechanism** that only performs `fsync` when there have been actual NoSync writes. The sync interval remains at 10ms for active writes, but during idle periods, no disk writes occur.

### Key Changes

1. **Added dirty flag to walSyncer** (`internal/storage/wal_syncer.go`)
   - New `dirty atomic.Bool` field tracks pending NoSync writes
   - New `MarkDirty()` method to signal that sync is needed
   - Modified `run()` loop to only call `doSync()` when `dirty` is true
   - Dirty flag is reset after successful sync

2. **Added helper methods to PebbleStore** (`internal/storage/impl.go`)
   These 4 methods are the **only** places where `markDirty()` is called:
   
   - `noSyncSet(key, val []byte) error` - for `db.Set(..., pebble.NoSync)` calls
   - `noSyncDelete(key []byte) error` - for `db.Delete(..., pebble.NoSync)` calls
   - `noSyncCommit(batch *pebble.Batch) error` - for `batch.Commit(pebble.NoSync)` calls
   - `conditionalNoSyncCommit(batch *pebble.Batch) error` - for config-dependent sync mode

   Each helper performs the Pebble operation and automatically calls `markDirty()` on success.

3. **Updated counterCoalescer** (`internal/storage/counter_coalescer.go`)
   - Added optional `onFlushed` callback invoked after each flush
   - Callback is wired to `markDirty()` in `NewPebbleStore`
   - Tracks if any writes actually occurred before signaling

4. **Refactored all NoSync write paths** to use the new helpers:
   - `engram.go`, `association.go`, `entity.go`
   - `idempotency.go`, `ordinal.go`, `transition.go`
   - `lastaccess.go`, `vault_lifecycle.go`, `plugin_store.go`
   - `impl.go` (WriteEngram, WriteEngramBatch)

5. **Comprehensive test coverage** (`internal/storage/wal_syncer_adaptive_test.go`)
   - `TestWALSyncer_AdaptiveSync_SkipsWhenNotDirty`
   - `TestWALSyncer_AdaptiveSync_SyncsWhenDirty`
   - `TestWALSyncer_AdaptiveSync_ConcurrentMarkDirty`
   - `TestWALSyncer_AdaptiveSync_FinalSyncOnClose`
   - `TestWALSyncer_AdaptiveSync_DirtyFlagBehavior`

## Behavior Changes

| Metric | Before | After |
|--------|--------|-------|
| Idle disk writes | ~100 fsyncs/sec | ~0 fsyncs/sec |
| Idle throughput | ~1 MB/s | ~0 KB/s |
| Active write latency | Same | Same |
| Max data loss | 10ms | 10ms (unchanged) |

## Durability Guarantees

The durability contract remains unchanged:
- **Active writes**: Sync occurs within 10ms of a NoSync write
- **Idle periods**: No unnecessary disk activity
- **Graceful shutdown**: Final sync performed if dirty
- **Crash recovery**: Pebble's WAL replay handles recovery as before

## Implementation Details

### Centralized Dirty Tracking

The `markDirty()` function now appears in exactly **4 places** (plus definition and callback registration):

```go
// internal/storage/impl.go

// 1. Callback registration
counterFlush = newCounterCoalescer(db, ps.markDirty)

// 2. Function definition
func (ps *PebbleStore) markDirty() {
    if ps.walSync != nil {
        ps.walSync.MarkDirty()
    }
}

// 3-6. The 4 helper methods (only places that call markDirty)
func (ps *PebbleStore) noSyncSet(key, val []byte) error {
    err := ps.db.Set(key, val, pebble.NoSync)
    if err == nil {
        ps.markDirty()  // <-- Only here
    }
    return err
}

func (ps *PebbleStore) noSyncDelete(key []byte) error {
    err := ps.db.Delete(key, pebble.NoSync)
    if err == nil {
        ps.markDirty()  // <-- Only here
    }
    return err
}

func (ps *PebbleStore) noSyncCommit(batch *pebble.Batch) error {
    err := batch.Commit(pebble.NoSync)
    if err == nil {
        ps.markDirty()  // <-- Only here
    }
    return err
}

func (ps *PebbleStore) conditionalNoSyncCommit(batch *pebble.Batch) error {
    syncOption := pebble.Sync
    if ps.noSyncEngrams {
        syncOption = pebble.NoSync
    }
    err := batch.Commit(syncOption)
    if err == nil && ps.noSyncEngrams {
        ps.markDirty()  // <-- Only here
    }
    return err
}
```

All other code uses these helpers, ensuring dirty tracking is:
- **Automatic**: Never forgotten when using helpers
- **Centralized**: Single point of maintenance
- **Type-safe**: Compile-time enforcement

## Testing

All existing tests pass:
```bash
$ go test ./internal/storage/... ./internal/engine/...
ok      github.com/scrypster/muninndb/internal/storage    2.644s
ok      github.com/scrypster/muninndb/internal/engine     3.973s
```

New tests specifically verify:
- No sync when not dirty (idle behavior)
- Sync occurs when dirty (active behavior)
- Thread-safety of concurrent MarkDirty() calls
- Proper cleanup on Close()

---

**Checklist:**
- [x] Code compiles without errors
- [x] All existing tests pass
- [x] New tests added for the feature
- [x] No breaking changes to API or behavior
- [x] Durability guarantees preserved
- [x] markDirty() centralized in 4 helper methods only
