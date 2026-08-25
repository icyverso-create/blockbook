package db

import "flag"
import "github.com/linxGnu/grocksdb"

var (
	noCompression = flag.Bool("noCompression", false, "disable rocksdb compression when rocksdb library can't find compression library linked with binary")
)

// RocksDB memory/file-size budget.
//
// Everything below exists to make RocksDB's footprint BOUNDED and PREDICTABLE. The
// motivating failure is a chain with multi-gigabyte blocks (Bitcoin SV): the host budget
// is consumed by block parsing, so RocksDB must fit in a fixed, known envelope instead of
// growing with the size of the database.
//
// Three leaks were closed here:
//
//  1. index and bloom-filter blocks lived OUTSIDE the LRU cache. With
//     cache_index_and_filter_blocks left at its RocksDB default (false), every open SST
//     pins its whole index + filter in the table reader, charged to nothing and bounded
//     only by -dbmaxopenfiles. The same configuration on a BSC node held roughly 80 GB
//     off-cache behind ~40000 open SSTs. They are now cached, and therefore capped by
//     -dbcache.
//
//  2. memtables were unbounded in aggregate: write_buffer_size is PER column family, and
//     a bitcoin-type chain opens nine of them (thirteen for an ethereum-type chain), so
//     the real ceiling was numCFs * write_buffer_size * max_write_buffer_number of memory
//     that -dbcache never accounted for - about 2.25 GiB at the previous 128 MiB.
//
//  3. blob files were never explicitly disabled, so the setting depended on the linked
//     RocksDB's default. A BSV database built by an earlier binary accumulated 10903 blob
//     files / 238 GB, two of them larger than 2^32 bytes (471844.blob was 2^32 + 269 kB),
//     which segfaulted inside cgo and left the database permanently unusable.
//
// None of this touches dbVersion or the column-family list, so no coin needs a resync.
const (
	// dbTargetFileSize caps a single SST produced by compaction. It equals
	// dbWriteBufferSize below, so files are uniform across the whole LSM tree: a flush
	// (L0) and a compaction (L1+) both emit ~64 MiB files. 64 MiB is also RocksDB's own
	// default; it is pinned explicitly so the value cannot drift with the linked RocksDB
	// version, and so the multiplier below cannot be assumed.
	//
	// The number matters for BSV specifically: a dead BSV database contained a single
	// 3.38 GiB SST, produced because one oversized WriteBatch became one oversized
	// memtable and therefore one oversized L0 file. target_file_size_base does not apply
	// to flush output, so it cannot prevent that L0 file (only a bounded WriteBatch can,
	// see the import path) - but it does guarantee such a file is broken back into
	// 64 MiB pieces by the first compaction and can never be recreated at L1 and below.
	dbTargetFileSize = 64 << 20 // 64 MiB

	// dbWriteBufferSize is the per-column-family memtable size, halved from the previous
	// 128 MiB back to RocksDB's own default. It is a per-CF number multiplied by nine
	// column families and by dbMaxWriteBufferNumber, and it directly sets the size of an
	// ordinary L0 file and of the memtable copy held during a flush. The trade - more L0
	// files and more compaction work - is a speed cost, which this chain can afford, in
	// exchange for memory, which it cannot.
	//
	// Together with dbTargetFileSize and dbMaxBytesForLevelBase this restores the
	// canonical RocksDB ratio 1 : 1 : 4 (memtable : SST : base level). The previous
	// 128 : 64 : 128 had a base level half the size of one memtable.
	dbWriteBufferSize = 64 << 20 // 64 MiB

	// dbMaxWriteBufferNumber is RocksDB's default, pinned explicitly because it is a
	// multiplier on the memory estimate: at most this many memtables per column family
	// exist at once (one live, one being flushed).
	dbMaxWriteBufferNumber = 2

	// dbTotalWriteBufferSize is a HARD cap on memtable memory summed over ALL column
	// families (db_write_buffer_size). Without it the ceiling is
	// 9 CFs * 64 MiB * 2 = 1.125 GiB; with it, RocksDB flushes the largest memtable as
	// soon as the total crosses 512 MiB. During sync only a few column families are hot,
	// so each still gets 128-256 MiB and no flush storm results. RocksDB's internal
	// write buffer manager for this option does not stall writers, it only schedules
	// early flushes, so this cannot deadlock the importer.
	dbTotalWriteBufferSize = 512 << 20 // 512 MiB

	// dbMaxBytesForLevelBase sizes the level that L0 is merged into. With
	// level_compaction_dynamic_level_bytes on (pinned below) it is the CEILING of that
	// base level's target, which RocksDB picks from the range
	// (value/max_bytes_for_level_multiplier, value]; with the option off it is the plain
	// L1 target. Either way it is the unit the whole LSM tree is scaled from.
	//
	// It is set to the volume of L0 data that triggers a compaction -
	// level0_file_num_compaction_trigger (4, RocksDB default) * dbWriteBufferSize =
	// 256 MiB - so the base level can hold one L0 merge without immediately spilling. The
	// previous 128 MiB was half of a single memtable, which scaled every level down by 2x
	// and cost roughly one extra level of read and write amplification on a database the
	// size of a BSV index.
	dbMaxBytesForLevelBase = 256 << 20 // 256 MiB

	// dbMetadataBlockSize is the size of one partition of a partitioned index/filter.
	// Partitioning is the point: a lookup faults in one partition instead of the whole
	// multi-megabyte filter of a 64 MiB SST, so cache pressure scales with the working
	// set rather than with the size of the database.
	//
	// 16 kB rather than RocksDB's 4 kB default because the top-level index over the
	// partitions is PINNED per open file and is therefore a non-evictable floor that
	// scales with -dbmaxopenfiles. A 64 MiB SST carries roughly 1.6 MB of 10-bit bloom
	// filter; at 4 kB that is ~400 partitions and a ~16 kB top-level block per file, or
	// ~256 MiB pinned across the 16384 files -dbmaxopenfiles allows by default. At 16 kB
	// it is ~100 partitions and ~4 kB per file, ~64 MiB pinned. The cost is faulting
	// 16 kB instead of 4 kB per filter miss - which lands in the bounded cache. Trading
	// an unevictable floor for evictable, accounted bytes is the whole point of this
	// file.
	dbMetadataBlockSize = 16 << 10 // 16 kB
)

// createAndSetDBOptions builds the per-column-family options.
//
// bloomBits <= 0 disables the bloom filter (used for the addresses column family, which is
// read by iterator rather than by point lookup). c is the shared LRU cache sized by
// -dbcache; every block-based cost below is charged to it. maxOpenFiles comes from
// -dbmaxopenfiles and now bounds only the small per-table residue, not the index/filter
// blocks.
func createAndSetDBOptions(bloomBits int, c *grocksdb.Cache, maxOpenFiles int) *grocksdb.Options {
	blockOpts := grocksdb.NewDefaultBlockBasedTableOptions()
	blockOpts.SetBlockSize(32 << 10) // 32kB
	blockOpts.SetBlockCache(c)
	if bloomBits > 0 {
		blockOpts.SetFilterPolicy(grocksdb.NewBloomFilter(float64(bloomBits)))
	}
	blockOpts.SetFormatVersion(4)

	// Charge index and filter blocks to the LRU cache. RocksDB's default is false, and
	// this is the single most important line in this file: without it those blocks are
	// allocated outside every accounting RocksDB does, and total RSS grows with the
	// number of open SSTs rather than with -dbcache.
	blockOpts.SetCacheIndexAndFilterBlocks(true)
	// ...but do not let a sequential scan of data blocks evict them. High-priority
	// entries get their own pool inside the LRU cache (NewLRUCache reserves half the
	// capacity for it, and grocksdb v1.9.8 exposes no setter for the ratio), so index and
	// filter blocks are only evicted once that pool is itself full. Already RocksDB's
	// default; stated explicitly because the option above is worthless without it.
	blockOpts.SetCacheIndexAndFilterBlocksWithHighPriority(true)

	// Partitioned index and filters. Now that index/filter blocks are cached and can be
	// evicted, they must be loadable in small pieces, otherwise every miss re-reads a
	// whole multi-megabyte index. Two-level index is a hard prerequisite of partitioned
	// filters: RocksDB rejects partition_filters without kTwoLevelIndexSearch at open
	// time. Partitioning is also what makes the pinning below cheap.
	// https://github.com/facebook/rocksdb/wiki/Partitioned-Index-Filters
	blockOpts.SetIndexType(grocksdb.KTwoLevelIndexSearchIndexType)
	blockOpts.SetMetadataBlockSize(dbMetadataBlockSize)
	if bloomBits > 0 {
		// Guarded: the addresses column family has no filter policy, and partitioning a
		// filter that does not exist is meaningless.
		blockOpts.SetPartitionFilters(true)
	}
	// Keep the two small, always-needed pieces resident instead of re-reading them: the
	// top-level index/filter of every partitioned table (RocksDB's default, pinned), and
	// the index/filter of L0 files, which are hot and few (not the default). With
	// partitioning, "pinned" means only the top-level block - kilobytes per file, not the
	// megabytes an unpartitioned pin would cost. That distinction matters after
	// --repair, which lands every recovered file in L0.
	blockOpts.SetPinTopLevelIndexAndFilter(true)
	// Deliberately NOT SetPinL0FilterAndIndexBlocksInCache: with partitioned
	// index/filter that pins every partition of every L0 file, not just the top
	// level, and those entries cannot be evicted. After a --repair, when the
	// whole database lands in L0, it would reinstate exactly the unbounded
	// off-budget growth this file exists to prevent.

	opts := grocksdb.NewDefaultOptions()
	opts.SetBlockBasedTableFactory(blockOpts)
	opts.SetCreateIfMissing(true)
	opts.SetCreateIfMissingColumnFamilies(true)
	opts.SetMaxBackgroundCompactions(6)
	opts.SetMaxBackgroundFlushes(6)
	opts.SetBytesPerSync(8 << 20) // 8MB

	// Memtables: bounded per column family and, more importantly, in aggregate.
	opts.SetWriteBufferSize(dbWriteBufferSize)
	opts.SetMaxWriteBufferNumber(dbMaxWriteBufferNumber)
	opts.SetDbWriteBufferSize(dbTotalWriteBufferSize)

	// SST sizing. The multiplier is pinned to 1 so file size does not grow with level:
	// a file at the bottom level is the same 64 MiB as a file at L1.
	opts.SetTargetFileSizeBase(dbTargetFileSize)
	opts.SetTargetFileSizeMultiplier(1)
	opts.SetMaxBytesForLevelBase(dbMaxBytesForLevelBase)
	// Size levels from the bottom up, which bounds space amplification to roughly 1.11x
	// instead of up to 2x. On a database the size of a BSV index, running out of disk is
	// a stability failure, not a performance one. This is RocksDB's own default since 8.0
	// (a no-op against the v9.10.0 this repo pins in build/docker/bin/Dockerfile) and is
	// stated explicitly only so the LSM shape, and therefore the meaning of
	// dbMaxBytesForLevelBase above, does not silently change with the linked library.
	// Since RocksDB 8.2 an existing database migrates to it on restart with no manual
	// compaction.
	opts.SetLevelCompactionDynamicLevelBytes(true)

	opts.SetMaxOpenFiles(maxOpenFiles)

	// Blob files: off, unconditionally and explicitly. Large values stay inline in the
	// SSTs. This is not a performance choice - blob files are what destroyed the previous
	// BSV database (a .blob past 2^32 bytes, then SIGSEGV inside cgo). Leaving it to the
	// linked RocksDB's default is exactly how that database acquired 238 GB of blobs from
	// an earlier binary. Blob GC is disabled with it so no blob machinery runs at all.
	opts.EnableBlobFiles(false)
	opts.EnableBlobGC(false)

	if *noCompression {
		// resolve error rocksDB: Invalid argument: Compression type LZ4HC is not linked with the binary
		opts.SetCompression(grocksdb.NoCompression)
	} else {
		opts.SetCompression(grocksdb.LZ4HCCompression)
	}
	return opts
}
