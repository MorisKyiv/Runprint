# Decision 0006: Bound passthrough shutdown after interruption

## Status

Accepted

## Context

Decision 0004 makes capture authoritative but deliberately leaves live output synchronous. That preserves normal pipeline backpressure: a command cannot outrun a slow downstream consumer.

An arbitrary `io.Writer` cannot be cancelled, however. If a passthrough write is blocked when the user interrupts the command, closing the capture pipe does not unblock that write. Waiting for the drain goroutine would then make the 500 ms drain deadline ineffective and could prevent Runprint from saving the interruption record.

## Decision

Normal command completion keeps synchronous backpressure and waits for both stream drains.

After Runprint has handled an interrupt or context cancellation, the drain deadline is a hard upper bound. At that deadline Runprint:

1. closes any unresolved capture reader;
2. marks its capture as incomplete;
3. marks configured passthrough for that stream as failed;
4. snapshots the bounded collector under its mutex; and
5. returns without waiting for an uninterruptible output writer.

The collector accepts each chunk before passthrough sees it. Its mutex makes a deadline snapshot safe while a drain goroutine is still inside the external writer. The buffered result channel also lets that goroutine finish without requiring Runprint to receive its late result.

## Consequences

- A blocked downstream consumer cannot prevent an interrupted record from being saved.
- The record explicitly says that the affected stream capture is incomplete, and the CLI emits its existing generic passthrough warning.
- Ordinary, non-interrupted pipelines keep backpressure; Runprint does not silently drop live output merely because a consumer is slow.
- Go cannot cancel an arbitrary blocked `io.Writer`. The abandoned drain goroutine may live until that writer returns; the CLI process exits immediately after saving the record.
- Bytes already accepted by the collector are retained. Bytes still buffered in the pipe at the deadline may be absent, which is why `capture_complete` is false.
