package harvestpy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// The pool's defaults. One worker holds the docling models (about 2 GB RSS)
// and runs its conversion on several threads, so the default bound is a
// quarter of the CPUs and of the memory, between one and eight workers.
const (
	defaultConversionTimeout = 180 * time.Second
	conversionTimeoutPerMiB  = 15 * time.Second
	ocrTimeoutPerMiB         = 60 * time.Second
	maxConversionTimeout     = 30 * time.Minute
	defaultIdleTimeout       = 10 * time.Minute
	converterWorkerBytes     = uint64(2) << 30
	maxDefaultWorkers        = 8
	unknownMemoryWorkers     = 2
	queuePerWorker           = 8
)

// ErrConverterBusy is a conversion refused because every worker is busy and
// the queue is full, or because no worker freed up within the queue wait: a
// named, immediate answer instead of a caller hanging behind the others. It
// names the server's load, not the document: the harvester's adapter marks the
// walk refused (harvest.NoteRefusedForLoad), so it is never cached as the
// source's failure.
var ErrConverterBusy = errors.New("harvestpy converter is saturated")

// ErrConverterTimeout is a conversion that ran past its own deadline
// (conversionBudget). Its worker is killed and replaced; the pool lives on.
var ErrConverterTimeout = errors.New("harvestpy conversion exceeded its deadline")

// ErrConverterDesync is a response line that is not JSON or answers another
// request's id: the pipe can no longer be trusted, so that worker is killed.
var ErrConverterDesync = errors.New("harvestpy worker answered out of protocol")

// errConverterClosed is a request to a shared pool its last holder closed, or
// one whose worker was still starting when a Close drained the pool.
var errConverterClosed = errors.New("harvestpy converter is closed")

// converterPoolSize is the default worker bound for a host with cpus CPUs and
// memory bytes of RAM (0 when unknown).
func converterPoolSize(cpus int, memory uint64) int {
	size := (cpus + 3) / 4
	if memory == 0 {
		size = min(size, unknownMemoryWorkers)
	} else {
		size = min(size, int(memory/4/converterWorkerBytes))
	}
	return max(1, min(size, maxDefaultWorkers))
}

// conversionBudget is one conversion's own deadline: base (0 is the default)
// plus an allowance per whole MiB of input, larger under OCR, where a scanned
// page costs seconds; capped at maxConversionTimeout, or at base when that is
// larger.
func conversionBudget(base time.Duration, ocr bool, size int64) time.Duration {
	if base <= 0 {
		base = defaultConversionTimeout
	}
	perMiB := conversionTimeoutPerMiB
	if ocr {
		perMiB = ocrTimeoutPerMiB
	}
	ceiling := max(base, maxConversionTimeout)
	return min(base+time.Duration(size>>20)*perMiB, ceiling)
}

// requestBudget is the deadline of one convert request: its input's size
// scales the base, and OCR applies when the request or the configuration
// (convert.pdfOcr, PDFs only) asks for it.
func requestBudget(runtime Runtime, request Request) time.Duration {
	var size int64
	if info, err := os.Stat(request.Path); err == nil {
		size = info.Size()
	}
	ocr := request.OCR || (runtime.PDFOCR && strings.EqualFold(strings.TrimPrefix(request.Kind, "."), "pdf"))
	return conversionBudget(runtime.Timeout, ocr, size)
}

// converterPool is the bounded set of conversion workers behind one
// Converter: lazily started, handed to one request at a time, reaped when idle,
// killed and replaced when a request times out, is cancelled or desyncs.
type converterPool struct {
	runtime   Runtime
	clock     clock.Clock
	size      int
	queueCap  int
	queueWait time.Duration
	idleTTL   time.Duration
	nextID    atomic.Uint64

	mu      sync.Mutex
	idle    []*pooledWorker
	all     map[*pooledWorker]struct{}
	live    int // started, starting or busy workers, idle ones included
	waiters []chan poolGrant
	// drains counts drain calls, so a worker whose start spanned one is
	// stopped instead of joining the pool behind drain's back.
	drains int
	// closed is a shared pool its registry forgot (releaseShared): it refuses
	// every request and keeps no worker.
	closed bool
}

// pooledWorker is one conversion worker process and its pool bookkeeping.
type pooledWorker struct {
	*workerProcess
	// idleCancel ends the reap wait of the worker's current idle spell.
	idleCancel context.CancelFunc
	// idleSpell counts the worker's returns to the idle list, so a reap timer
	// from an earlier spell never stops it in a later one.
	idleSpell int
	served    int
	retired   bool
	stopOnce  sync.Once
	stopErr   error
}

// stop ends the worker's process once, however many paths reach it.
func (worker *pooledWorker) stop() error {
	worker.stopOnce.Do(func() { worker.stopErr = stopWorkerProcess(worker.workerProcess, "converter worker") })
	return worker.stopErr
}

// poolGrant is what a queued request receives: an idle worker, a slot to start
// one (worker nil), or the pool's Close (closed).
type poolGrant struct {
	worker *pooledWorker
	closed bool
}

func newConverterPool(runtime Runtime) *converterPool {
	size := runtime.Workers
	if size <= 0 {
		size = converterPoolSize(goruntime.NumCPU(), physicalMemoryBytes())
	}
	queueCap := runtime.Queue
	if queueCap <= 0 {
		queueCap = queuePerWorker * size
	}
	queueWait := runtime.Timeout
	if queueWait <= 0 {
		queueWait = defaultConversionTimeout
	}
	idleTTL := runtime.IdleTimeout
	if idleTTL <= 0 {
		idleTTL = defaultIdleTimeout
	}
	return &converterPool{
		runtime: runtime, clock: clock.Real, size: size, queueCap: queueCap, queueWait: queueWait, idleTTL: idleTTL,
		all: map[*pooledWorker]struct{}{},
	}
}

// queued is the number of requests waiting for a worker.
func (pool *converterPool) queued() int {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.waiters)
}

// acquire answers a worker for one request: an idle one, a new one while the
// pool is under its bound, else a place in the bounded queue until a worker
// frees up, the queue wait ends (ErrConverterBusy) or ctx does. depth is the
// queue length the request found.
func (pool *converterPool) acquire(ctx context.Context) (worker *pooledWorker, depth int, err error) {
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil, 0, errConverterClosed
	}
	if count := len(pool.idle); count > 0 {
		worker = pool.idle[count-1]
		pool.idle = pool.idle[:count-1]
		worker.idleCancel()
		pool.mu.Unlock()
		return worker, 0, nil
	}
	if pool.live < pool.size {
		pool.live++
		pool.mu.Unlock()
		worker, err = pool.start()
		return worker, 0, err
	}
	depth = len(pool.waiters)
	if depth >= pool.queueCap {
		pool.mu.Unlock()
		return nil, depth, fmt.Errorf("%w: all %d workers are busy and %d conversions are already queued",
			ErrConverterBusy, pool.size, depth)
	}
	grants := make(chan poolGrant, 1)
	pool.waiters = append(pool.waiters, grants)
	pool.mu.Unlock()
	timer := pool.clock.NewTimer(pool.queueWait)
	defer timer.Stop()
	select {
	case grant := <-grants:
		worker, err = pool.take(grant)
		return worker, depth, err
	case <-ctx.Done():
		err = fmt.Errorf("harvestpy worker request cancelled while queued: %w", ctx.Err())
	case <-timer.C():
		err = fmt.Errorf("%w: no worker of %d freed up within %s", ErrConverterBusy, pool.size, pool.queueWait)
	}
	pool.mu.Lock()
	for index, waiting := range pool.waiters {
		if waiting == grants {
			pool.waiters = append(pool.waiters[:index], pool.waiters[index+1:]...)
			pool.mu.Unlock()
			return nil, depth, err
		}
	}
	pool.mu.Unlock()
	// A grant raced the withdrawal: pass it on to whoever is next.
	if grant := <-grants; !grant.closed {
		if grant.worker != nil {
			pool.release(grant.worker)
		} else {
			pool.slotFreed()
		}
	}
	return nil, depth, err
}

// take turns a queued request's grant into its worker.
func (pool *converterPool) take(grant poolGrant) (*pooledWorker, error) {
	switch {
	case grant.closed:
		return nil, errors.New("harvestpy converter closed while the conversion was queued")
	case grant.worker != nil:
		return grant.worker, nil
	default:
		return pool.start()
	}
}

// start spawns one worker into a slot already counted in live; a failed
// start hands the slot back. A worker whose start spanned a drain (Close) is
// stopped and its request refused, as drain does to a busy worker.
func (pool *converterPool) start() (*pooledWorker, error) {
	pool.mu.Lock()
	drains := pool.drains
	pool.mu.Unlock()
	process, err := startConverterWorker(pool.runtime)
	if err != nil {
		pool.slotFreed()
		return nil, err
	}
	worker := &pooledWorker{workerProcess: process}
	pool.mu.Lock()
	if pool.drains != drains || pool.closed {
		pool.mu.Unlock()
		_ = worker.stop()
		pool.slotFreed()
		return nil, fmt.Errorf("%w while its worker was starting", errConverterClosed)
	}
	pool.all[worker] = struct{}{}
	pool.mu.Unlock()
	return worker, nil
}

// replace swaps a dead worker for a fresh one in the same slot, so the request
// holding it keeps its place instead of queueing again; after a Close it
// discards the worker and refuses.
func (pool *converterPool) replace(worker *pooledWorker) (*pooledWorker, error) {
	_ = worker.stop()
	pool.mu.Lock()
	delete(pool.all, worker)
	if worker.retired || pool.closed {
		pool.mu.Unlock()
		pool.slotFreed()
		return nil, errConverterClosed
	}
	pool.mu.Unlock()
	return pool.start()
}

// release returns a healthy worker: to the head of the queue, else to the idle
// list with its reap timer armed. A worker Close retired is stopped instead.
func (pool *converterPool) release(worker *pooledWorker) {
	pool.mu.Lock()
	if worker.retired || pool.closed {
		pool.mu.Unlock()
		pool.discard(worker)
		return
	}
	if len(pool.waiters) > 0 {
		next := pool.waiters[0]
		pool.waiters = pool.waiters[1:]
		next <- poolGrant{worker: worker}
		pool.mu.Unlock()
		return
	}
	pool.idle = append(pool.idle, worker)
	worker.idleSpell++
	spell := worker.idleSpell
	idle, cancel := context.WithCancel(context.Background())
	worker.idleCancel = cancel
	pool.mu.Unlock()
	go func() {
		if pool.clock.Sleep(idle, pool.idleTTL) == nil {
			pool.reap(worker, spell)
		}
	}()
}

// reap stops a worker that sat idle past idleTTL in spell, unless a request
// took it in the meantime.
func (pool *converterPool) reap(worker *pooledWorker, spell int) {
	pool.mu.Lock()
	if worker.idleSpell != spell {
		pool.mu.Unlock()
		return
	}
	for index, idle := range pool.idle {
		if idle != worker {
			continue
		}
		pool.idle = append(pool.idle[:index], pool.idle[index+1:]...)
		delete(pool.all, worker)
		pool.live--
		pool.mu.Unlock()
		_ = worker.stop()
		return
	}
	pool.mu.Unlock()
}

// discard kills a worker that can no longer be trusted (a timeout, a cancel,
// a desync, a broken pipe) and frees its slot for a replacement.
func (pool *converterPool) discard(worker *pooledWorker) {
	_ = worker.stop()
	pool.mu.Lock()
	delete(pool.all, worker)
	pool.mu.Unlock()
	pool.slotFreed()
}

// slotFreed hands a freed slot to the head of the queue (which starts a
// worker in it), else gives it back.
func (pool *converterPool) slotFreed() {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.waiters) > 0 {
		next := pool.waiters[0]
		pool.waiters = pool.waiters[1:]
		next <- poolGrant{}
		return
	}
	pool.live--
}

// drain stops every worker — idle ones now, busy ones by killing them under
// their request, which then fails — and refuses every queued request. The
// pool stays usable: the next request starts a fresh worker.
func (pool *converterPool) drain() error {
	pool.mu.Lock()
	pool.drains++
	workers := make([]*pooledWorker, 0, len(pool.all))
	for worker := range pool.all {
		worker.retired = true
		workers = append(workers, worker)
	}
	for _, worker := range pool.idle {
		worker.idleCancel()
		delete(pool.all, worker)
		pool.live--
	}
	pool.idle = nil
	for _, waiting := range pool.waiters {
		waiting <- poolGrant{closed: true}
	}
	pool.waiters = nil
	pool.mu.Unlock()
	var drainErr error
	for _, worker := range workers {
		drainErr = errors.Join(drainErr, worker.stop())
	}
	return drainErr
}

// exchangeVerdict is what a finished exchange left its worker fit for.
type exchangeVerdict int

const (
	workerHealthy exchangeVerdict = iota
	workerBroken
	// workerUnreachable: the request never reached a reused worker (its process
	// died while idle); nothing ran, so the request may go to another one.
	workerUnreachable
)

// request runs one protocol request on a pooled worker under budget (0: the
// caller's context alone bounds it). A request whose write never reached a
// reused worker is retried once on a fresh worker in the same slot.
func (converter *Converter) request(
	ctx context.Context,
	body []byte,
	budget time.Duration,
) (line []byte, tail string, returnErr error) {
	pool := converter.pool
	queuedAt := pool.clock.Now()
	worker, depth, err := pool.acquire(ctx)
	if err != nil {
		return nil, "", err
	}
	waited := pool.clock.Now().Sub(queuedAt)
	for attempt := 0; ; attempt++ {
		line, tail, verdict, err := pool.exchange(ctx, worker, body, budget, waited, depth)
		if verdict == workerUnreachable && attempt == 0 && ctx.Err() == nil {
			if worker, err = pool.replace(worker); err != nil {
				return nil, tail, err
			}
			continue
		}
		if verdict == workerHealthy {
			worker.served++
			pool.release(worker)
		} else {
			pool.discard(worker)
		}
		return line, tail, err
	}
}

// exchange writes one request line carrying a fresh id and reads its one
// response line, both bounded by ctx and the request's own deadline.
func (pool *converterPool) exchange(
	ctx context.Context,
	worker *pooledWorker,
	body []byte,
	budget time.Duration,
	waited time.Duration,
	depth int,
) (line []byte, tail string, verdict exchangeVerdict, returnErr error) {
	id := pool.nextID.Add(1)
	pool.mu.Lock()
	live := pool.live
	pool.mu.Unlock()
	end := worker.obs.Request("convert",
		slog.Int64("wait_ms", waited.Milliseconds()), slog.Int("queue", depth), slog.Int("workers", live))
	defer func() { end(len(line), returnErr) }()
	payload, err := withRequestID(body, id)
	if err != nil {
		return nil, "", workerHealthy, err
	}
	requestCtx := ctx
	if budget > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	interrupted := func() error {
		if ctx.Err() == nil {
			return fmt.Errorf("%w: no answer within %s; the worker was killed and is replaced",
				ErrConverterTimeout, budget)
		}
		return fmt.Errorf("harvestpy worker request cancelled: %w", ctx.Err())
	}
	writeResult := make(chan error, 1)
	go func() {
		_, err := worker.stdin.Write(payload)
		writeResult <- err
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			stderr := stderrTail(worker.stderr.String())
			verdict = workerBroken
			if worker.served > 0 {
				verdict = workerUnreachable
			}
			return nil, stderr, verdict, fmt.Errorf("harvestpy worker write failed: %w (stderr: %s)", err, stderr)
		}
	case <-requestCtx.Done():
		return nil, "", workerBroken, interrupted()
	}
	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := readLineBounded(worker.stdout, converterResponseLimit)
		read <- readResult{line: bytes.TrimSpace(line), err: err}
	}()
	select {
	case <-requestCtx.Done():
		return nil, "", workerBroken, interrupted()
	case response := <-read:
		stderr := stderrTail(worker.stderr.String())
		if response.err != nil {
			return nil, stderr, workerBroken, fmt.Errorf("harvestpy worker read failed: %w (stderr: %s)",
				response.err, stderr)
		}
		if err := checkResponseID(response.line, id); err != nil {
			return nil, stderr, workerBroken, fmt.Errorf("%w: %v (stderr: %s)", ErrConverterDesync, err, stderr)
		}
		return response.line, stderr, workerHealthy, nil
	}
}

// withRequestID stamps id into one JSON-object request body.
func withRequestID(body []byte, id uint64) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, fmt.Errorf("harvestpy request is not a JSON object: %.40q", trimmed)
	}
	stamped := []byte(`{"id":` + strconv.FormatUint(id, 10))
	if rest := bytes.TrimSpace(trimmed[1:]); len(rest) > 1 {
		stamped = append(stamped, ',')
	}
	stamped = append(stamped, trimmed[1:]...)
	return append(stamped, '\n'), nil
}

// checkResponseID accepts a response line that is one JSON object answering
// id. A worker that echoes no id (a converter.py older than the echo, left by
// an install that skipped the harvester) is accepted; any other id is not.
func checkResponseID(line []byte, id uint64) error {
	var envelope struct {
		ID *uint64 `json:"id"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return fmt.Errorf("undecodable response line (%d bytes): %w", len(line), err)
	}
	if envelope.ID != nil && *envelope.ID != id {
		return fmt.Errorf("response answers request %d, want %d", *envelope.ID, id)
	}
	return nil
}

// sharedPools are the process-wide pools SharedConverter hands out, one per
// distinct runtime, so every service in one daemon shares one worker bound.
var sharedPools struct {
	sync.Mutex
	entries []*sharedPool
}

type sharedPool struct {
	runtime Runtime
	pool    *converterPool
	holders int
}

// SharedConverter answers a converter on the process-wide pool for runtime,
// creating it on first use; the pool's workers stop when its last holder
// closes. The loopback and external harvester services of one daemon each
// hold one, so together they never run more than one pool's workers.
func SharedConverter(runtime Runtime) *Converter {
	sharedPools.Lock()
	defer sharedPools.Unlock()
	for _, entry := range sharedPools.entries {
		if sameRuntime(entry.runtime, runtime) {
			entry.holders++
			return &Converter{runtime: runtime, pool: entry.pool, shared: entry}
		}
	}
	entry := &sharedPool{runtime: runtime, pool: newConverterPool(runtime), holders: 1}
	sharedPools.entries = append(sharedPools.entries, entry)
	return &Converter{runtime: runtime, pool: entry.pool, shared: entry}
}

// releaseShared drops one holder of entry, closing and forgetting its pool
// with the last: a converter still holding it is refused from then on.
func releaseShared(entry *sharedPool) error {
	sharedPools.Lock()
	entry.holders--
	if entry.holders > 0 {
		sharedPools.Unlock()
		return nil
	}
	for index, candidate := range sharedPools.entries {
		if candidate == entry {
			sharedPools.entries = append(sharedPools.entries[:index], sharedPools.entries[index+1:]...)
			break
		}
	}
	sharedPools.Unlock()
	entry.pool.mu.Lock()
	entry.pool.closed = true
	entry.pool.mu.Unlock()
	return entry.pool.drain()
}

// sameRuntime compares two runtimes field by field; runners compare by
// identity, and a runner whose dynamic type cannot be compared never matches.
func sameRuntime(left, right Runtime) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return left.Python == right.Python && left.Script == right.Script && left.PDFOCR == right.PDFOCR &&
		left.PDFLayout == right.PDFLayout && left.ModelRoot == right.ModelRoot &&
		left.ModelStaging == right.ModelStaging && left.Workers == right.Workers && left.Queue == right.Queue &&
		left.Timeout == right.Timeout && left.IdleTimeout == right.IdleTimeout && left.Runner == right.Runner
}
