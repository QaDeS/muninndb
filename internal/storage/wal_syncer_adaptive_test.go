package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
)

// TestWALSyncer_AdaptiveSync_SkipsWhenNotDirty verifies that the walSyncer
// does NOT perform an fsync when the dirty flag is false.
func TestWALSyncer_AdaptiveSync_SkipsWhenNotDirty(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)
	defer syncer.Close()

	// Wait for at least one ticker cycle
	time.Sleep(20 * time.Millisecond)

	// The dirty flag should be false since no writes occurred
	if syncer.dirty.Load() {
		t.Error("expected dirty flag to be false after idle period")
	}
}

// TestWALSyncer_AdaptiveSync_SyncsWhenDirty verifies that the walSyncer
// performs an fsync when the dirty flag is true.
func TestWALSyncer_AdaptiveSync_SyncsWhenDirty(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)
	defer syncer.Close()

	// Perform a NoSync write and mark dirty
	key := []byte("test-key")
	val := []byte("test-value")
	if err := db.Set(key, val, pebble.NoSync); err != nil {
		t.Fatalf("failed to set key: %v", err)
	}

	// Mark the syncer as dirty
	syncer.MarkDirty()

	if !syncer.dirty.Load() {
		t.Error("expected dirty flag to be true after MarkDirty()")
	}

	// Wait for sync to happen (should happen within one ticker cycle)
	time.Sleep(20 * time.Millisecond)

	// After sync, dirty flag should be reset
	if syncer.dirty.Load() {
		t.Error("expected dirty flag to be false after sync")
	}
}

// TestWALSyncer_AdaptiveSync_MultipleMarkDirtyCalls verifies that multiple
// MarkDirty() calls don't cause issues and the sync still happens.
func TestWALSyncer_AdaptiveSync_MultipleMarkDirtyCalls(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)
	defer syncer.Close()

	// Mark dirty multiple times rapidly
	for i := 0; i < 10; i++ {
		syncer.MarkDirty()
	}

	if !syncer.dirty.Load() {
		t.Error("expected dirty flag to be true after MarkDirty() calls")
	}

	// Wait for sync
	time.Sleep(20 * time.Millisecond)

	if syncer.dirty.Load() {
		t.Error("expected dirty flag to be false after sync")
	}
}

// TestWALSyncer_AdaptiveSync_ConcurrentMarkDirty verifies thread-safety
// of MarkDirty() when called concurrently.
func TestWALSyncer_AdaptiveSync_ConcurrentMarkDirty(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)
	defer syncer.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer.MarkDirty()
		}()
	}
	wg.Wait()

	if !syncer.dirty.Load() {
		t.Error("expected dirty flag to be true after concurrent MarkDirty() calls")
	}
}

// TestWALSyncer_AdaptiveSync_FinalSyncOnClose verifies that Close()
// performs a final sync if dirty, even without ticker firing.
func TestWALSyncer_AdaptiveSync_FinalSyncOnClose(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)

	// Mark dirty but don't wait for ticker
	syncer.MarkDirty()

	// Close should trigger final sync
	syncer.Close()

	// After close, dirty should be false (sync happened and flag was reset)
	if syncer.dirty.Load() {
		t.Error("expected dirty flag to be false after Close() with final sync")
	}
}

// TestWALSyncer_AdaptiveSync_NoSyncWhenNotDirtyOnClose verifies that Close()
// skips the final sync if not dirty (nothing to flush).
func TestWALSyncer_AdaptiveSync_NoSyncWhenNotDirtyOnClose(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)

	// Don't mark dirty - close should skip sync
	syncer.Close()

	// Should close cleanly without issues
	if syncer.stopped.Load() != true {
		t.Error("expected syncer to be stopped after Close()")
	}
}

// TestWALSyncer_AdaptiveSync_DirtyFlagBehavior verifies the core dirty flag mechanism.
func TestWALSyncer_AdaptiveSync_DirtyFlagBehavior(t *testing.T) {
	db := openTestPebble(t)
	syncer := newWALSyncer(db)
	defer syncer.Close()

	// Initially dirty should be false
	if syncer.dirty.Load() {
		t.Error("expected dirty to be false initially")
	}

	// After MarkDirty, should be true
	syncer.MarkDirty()
	if !syncer.dirty.Load() {
		t.Error("expected dirty to be true after MarkDirty")
	}

	// Wait for sync and reset
	time.Sleep(20 * time.Millisecond)

	// After sync, dirty should be false again
	if syncer.dirty.Load() {
		t.Error("expected dirty to be false after sync")
	}
}
