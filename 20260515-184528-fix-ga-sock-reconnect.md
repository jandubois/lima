# Deep Review: 20260515-184528-fix-ga-sock-reconnect

| | |
|---|---|
| **Date** | 2026-05-15 18:45 |
| **Repo** | [lima-vm/lima](https://github.com/lima-vm/lima) |
| **Round** | 1 (of fix/ga-sock-reconnect) |
| **Author** | [@mn-ram](https://github.com/mn-ram) |
| **Branch** | `fix/ga-sock-reconnect` |
| **Commits** | `a4755d7b` hostagent: stop destroying ga.sock on guest-agent reconnect |
| **Reviewers** | Claude Opus 4.7 (effort: xhigh), Codex GPT 5.5 (effort: xhigh), Gemini 3.1 Pro (effort: default), Qwen3 Coder Next (effort: high), Gemma4 31B (effort: high), GLM-4.7 Flash (effort: high) |
| **Verdict** | **Merge with fixes** — the `context.Background()` asymmetry inside `reForwardGuestAgentSock` and the missing `gaSockForwardMu` on the shutdown cleanup callsite are both worth addressing before merge; the rest are nice-to-have. |
| **Wall-clock time** | `1 h 33 min 50 s` |

---

## Executive Summary

This single-commit branch replaces two direct `forwardSSH(verbForward)` calls in the guest-agent reconnect path with a new `reForwardGuestAgentSock` helper that issues a best-effort `ssh -O cancel` before the new `ssh -O forward`, serialized by a new `gaSockForwardMu`. The fix correctly addresses the root cause of #2227 — duplicate ControlMaster registration causing the failure branch of `forwardSSH` to unlink `ga.sock` twice and leave it permanently missing.

Two follow-ups worth addressing before merge: (1) the new helper uses `context.Background()` for `verbCancel` while the subsequent `verbForward` uses the caller's live `ctx`, which is an avoidable foot-gun for a live reconnect path (four of the six reviewers raised this in some form, and an upstream PR comment already flagged it); (2) the shutdown cleanup callsite at `hostagent.go:728` calls `forwardSSH(verbCancel)` against the same `localUnix` without acquiring `gaSockForwardMu`, leaving a brief race window between shutdown and an in-flight `reForwardGuestAgentSock`.

Structurally one commit, one file, one focused fix; commit message includes a deterministic reproducer; new code is well-documented.

---

## Critical Issues

None.

---

## Important Issues

I1. **`gaSockForwardMu` not held by the shutdown cleanup callsite** — `pkg/hostagent/hostagent.go:727-731` [Claude Opus 4.7, Gemini 3.1 Pro]

```go
if a.driver.ForwardGuestAgent() {
    if err := forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false); err != nil {
        errs = append(errs, err)
    }
}
```

The new mutex is documented (lines 80-84) as serializing all `(re-)establishment of the SSH forward for the guest-agent unix socket`, but the cleanup closure registered around line 714 calls `forwardSSH(... verbCancel, false)` on the same `localUnix` without acquiring it. `forwardSSH`'s `verbCancel` path unlinks `localUnix` (via the deferred `os.RemoveAll`), so during shutdown — which runs before the live goroutines observe `ctx` cancellation — `cleanUp` can race a still-running `reForwardGuestAgentSock` on the very file the mutex was added to protect.

Fix: take the mutex around the cleanup call, or narrow the mutex docstring to explicitly exclude shutdown (with a brief note explaining why the race is bounded).

```diff
 		if a.driver.ForwardGuestAgent() {
+			a.gaSockForwardMu.Lock()
 			if err := forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false); err != nil {
 				errs = append(errs, err)
 			}
+			a.gaSockForwardMu.Unlock()
 		}
```

---

## Suggestions

S1. **`reForwardGuestAgentSock`'s `verbCancel` log line is misleading on a fresh start** — `pkg/hostagent/hostagent.go:984` [Claude Opus 4.7]

```go
_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

`forwardSSH(..., verbCancel, false)` unconditionally logs `Stopping forwarding %q (guest) to %q (host)` at `Infof` and defers `os.RemoveAll(local)`. On a fresh VM there is nothing to stop, but the operator still sees the misleading `Stopping forwarding ...` line followed immediately by the matching `Forwarding ...` line — and the same pair repeats on every 10-second reconnect tick while the guest agent is down. Functionally harmless; cosmetically the code looks like it is fighting itself.

Fix: downgrade the unix-socket `Stopping forwarding` log line in `forwardSSH` from `Infof` to `Debugf` (also quiets the analogous noise from the existing cleanup callsite at line 728), or gate the cancel on a `bool` so the very first call per process skips it.

S2. **`verbCancel` uses `context.Background()`; `verbForward` uses live `ctx`** — `pkg/hostagent/hostagent.go:984-985` [Claude Opus 4.7, Codex GPT 5.5, Gemini 3.1 Pro, Qwen3 Coder Next]

```go
_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
if err := forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbForward, false); err != nil {
```

The shutdown cleanup uses `context.Background()` because by then `ctx` has been cancelled (the comment at line 721 spells this out). `reForwardGuestAgentSock` borrowed the same pattern, but it runs from the live reconnect loop with a non-cancelled `ctx`. Two consequences worth weighing:

(a) If the underlying `ssh -O cancel` hangs (rare, but possible when the ControlMaster is in a bad state), the goroutine holds `gaSockForwardMu` indefinitely while waiting on an `exec.CommandContext` that cannot be interrupted. The hung goroutine never returns; the next reconnect tick on the main loop blocks on the mutex. Gemini graded this Important; Codex graded it Suggestion; Claude graded it Suggestion with a comment-only fix suggestion.

(b) An upstream PR comment already flagged the same asymmetry (lima#4911 inline review). It is treated here as Suggestion to match the consolidator's gate analysis — the practical hang is real but bounded (single-goroutine, not cross-goroutine deadlock without other bugs) — but the reasoning belongs in the diff regardless of which severity it lands at.

Fix: pass `ctx` to both calls, or keep `context.Background()` for `verbCancel` and add a one-line comment explaining the asymmetry on purpose. Either resolves the foot-gun.

```diff
-	_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
+	_ = forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

S3. **Inotify goroutine and main loop both call `reForwardGuestAgentSock` at startup** — `pkg/hostagent/hostagent.go:735-758, 773-779` [Gemini 3.1 Pro]

```go
client := a.getClient()
if client == nil || !isGuestAgentSocketAccessible(ctx, client) {
    if a.driver.ForwardGuestAgent() {
        a.reForwardGuestAgentSock(ctx, localUnix, remoteUnix)
    }
}
```

At startup both goroutines see `client == nil` and both call `reForwardGuestAgentSock`. The mutex serializes them, but the second caller still performs a full cancel-then-forward dance against the listener the first caller just installed. Dialers hitting `ga.sock` during the second caller's `verbCancel`-defer-`RemoveAll` window see `ENOENT`.

Fix (out of scope for this PR but worth noting): have the inotify goroutine wait on `a.guestAgentAliveCh` before doing anything socket-related. Forward setup then has a single owner (the main reconnect loop), `gaSockForwardMu` becomes defense-in-depth, and the brief startup unavailability window disappears.

S4. **No automated coverage for the reconnect path being fixed** — `pkg/hostagent/hostagent.go:977-988` [Gemini 3.1 Pro]

The commit message gives a deterministic shell reproducer (`limactl shell default -- sudo systemctl restart lima-guestagent.service; sleep 12; ls .../ga.sock`). A BATS test that automates that recipe would catch a regression of this exact bug as well as any future regression in concurrent inotify-goroutine restart. Real SSH and a real ControlMaster are required, so a Go unit test against the helper directly would not exercise the failure mode that motivated the PR.

---

## Design Observations

### Concerns

**(future)** **Inotify goroutine and main reconnect loop both racing the helper at startup is structural.** [Claude Opus 4.7]
The fix protects against corruption via the mutex but does not eliminate the brief startup window during which the second caller tears down and re-establishes a listener the first caller just installed. A cleaner shape (have the inotify goroutine wait on `a.guestAgentAliveCh` before doing anything socket-related) would give the forward setup a single owner. Out of scope for a focused bugfix PR — surfaced as S3 for the same reason.

**(future)** **User-defined socket forwards are not re-established when `ga.sock` is.** [Qwen3 Coder Next]
The new helper only handles the guest-agent socket. User-defined socket forwards set up once at lines 703-708 are not re-established when the reconnect loop re-forwards `ga.sock`. Strictly out of scope for the current bug (which is about `ga.sock` specifically), but worth tracking as a related cleanup if the project ever decides user sockets should also survive guest-agent restarts.

### Strengths

**(in-scope)** **Cancel-before-forward is the right pattern.** [Codex GPT 5.5, Gemini 3.1 Pro, Qwen3 Coder Next]
Addressing the duplicate-registration condition directly, rather than only preserving the filesystem socket path, removes the failure mode at its source.

**(in-scope)** **Scope of the mutex is narrow.** [Codex GPT 5.5, Qwen3 Coder Next]
The dedicated `gaSockForwardMu` field avoids broadening synchronization around unrelated SSH socket forwarding. Easy to reason about; easy to widen later if needed.

**(in-scope)** **Helper docstring spells out the failure mode.** [Claude Opus 4.7, Codex GPT 5.5, Gemini 3.1 Pro, Qwen3 Coder Next]
The `reForwardGuestAgentSock` comment block walks the reader through duplicate registration → failure branch `RemoveAll` → permanently missing `ga.sock`, with a reference to #2227. Future readers will not have to re-derive the chain.

**(in-scope)** **Both reconnect call sites are routed through the same helper.** [Claude Opus 4.7]
Centralizes the cancel-then-forward + locking contract; copy-paste is the obvious failure mode this avoids.

**(in-scope)** **Deterministic reproducer in the commit message.** [Claude Opus 4.7]
`limactl shell ... sudo systemctl restart lima-guestagent.service; sleep 12; ls .../ga.sock` is exactly the shape needed for an SSH-bound bug without automated coverage.

---

## Testing Assessment

No tests were added or modified. The bug requires a real SSH ControlMaster and a restartable guest agent, so a Go unit test against the new helper would not exercise the failure mode that motivated the PR. The deterministic shell reproducer in the commit message stands as the minimum evidence; promoting it to a BATS test that stops `lima-guestagent.service`, waits, restarts it, and asserts `ga.sock` is present would lock in regression coverage — see S5.

Untested scenarios ranked by likelihood of regression:

1. Guest-agent restart loop on a long-running instance (the exact bug being fixed).
2. VM reboot recovery using the same code path.
3. Concurrent inotify-goroutine restart racing the main reconnect loop on `gaSockForwardMu` (the mutex's reason for existing).
4. `ssh -O cancel` or `ssh -O forward` hanging while the host-agent context is cancelled (the S2 deadlock scenario).
5. WSL2 driver path — `ForwardGuestAgent()` returns true; `ssh -O cancel` reliability depends on which SSH toolchain is installed.

---

## Documentation Assessment

The new field comment (lines 80-84) and the new helper docstring (lines 956-976) are clear and well-targeted. One small accuracy gap (the docstring implies the mutex covers all access to that path, but the shutdown cleanup callsite at line 728 is outside its coverage) is captured in I1's reconciliation note — the fix is either lock the cleanup callsite or tighten the docstring.

---

## Commit Structure

Clean. One commit, one focused fix, DCO sign-off present, message walks through both the failure mode and the fix.

---

## Acknowledged Limitations

- The pre-existing `// TODO: use vSock (when QEMU for macOS gets support for vSock)` at `pkg/hostagent/hostagent.go:697` is unchanged by this PR.
- The cleanup callsites at lines 722 and 728 intentionally tolerate `verbCancel` errors; that decision is unchanged and orthogonal to S2 (which is about context lifetime).

---

## Unresolved Feedback

- [lima#4911 inline review](https://github.com/lima-vm/lima/pull/4911#discussion_r3244931774) — reviewer questioned why the new cancel call uses `context.Background()` instead of `ctx`. Covered by S2.

---

## Agent Performance Retro

### [Claude]

Read the file, the mutex docstring, and the surrounding cleanup callsites; landed on a careful four-Suggestion review with one substantive design observation about the startup race that nobody else surfaced. Severity calibration was on the cooler side — the context-asymmetry observation was Suggestion rather than Important — which proved closer to the gate-analysis verdict than Gemini's Important framing.

### [Codex]

Most focused contribution: one Suggestion, plus the only agent to explicitly cite the upstream PR comment about the same context asymmetry as Unresolved Feedback. Anchoring the consolidator to existing reviewer concerns was useful in its own right. Missed the mutex-on-cleanup gap that Claude and Gemini both caught.

### [Gemini]

Most aggressive grading — two Important findings, both real. Caught the mutex-missing-on-cleanup callsite (I1 in the consolidated report) and the context-asymmetry concern that the other agents graded Suggestion. Gate analysis downgraded the context concern to S2 but kept the framing.

### [Qwen]

Produced one text emission with zero tool calls — reasoned from the inlined diff alone. Raised the `context.Background`/`ctx` asymmetry as C1, downgraded by gate analysis to align with S2. No false positives. One self-contradiction: the C1 says use `ctx`, yet the Documentation Assessment paragraph calls `context.Background()` "intentionally used." Net contribution: one finding, fully overlapping the cloud trio's S2.

### [Gemma]

Produced a complete structured review (~2 KB) with zero findings at any severity. The Strengths section accurately describes the fix; the Testing Assessment flags "ControlMaster Failures" as a risk. No bugs surface. Net contribution: zero.

### [GLM]

Produced an effectively empty review. The sqlite session shows 3,166 characters of reasoning across three thinking blocks and three tool calls, then the agent loop stopped with 254 output tokens that opencode classified as reasoning rather than text. The extractor (which only catches text events) wrote a near-empty file containing just the model's opening "I'll perform a thorough review…" announcement. `--variant high` exhausted GLM's thinking budget before any usable answer landed.

### Summary

| | Claude Opus 4.7 | Codex GPT 5.5 | Gemini 3.1 Pro | Qwen3 Coder Next | Gemma4 31B | GLM-4.7 Flash |
|---|---|---|---|---|---|---|
| Duration | 2m 08s | 4m 04s | — | ~6m | ~6m | ~25m (empty) |
| Findings | 1I 2S | 1S | 1I 3S | 1S | none | none |
| Tool calls | 17 (Bash 13, Read 3, Grep 1) | 34 (shell 33, stdin 1) | — | 0 | 1 (Read) | 3 (Read 1, Bash 2) |
| Design observations | 4 | 2 | 2 | 2 | 3 | 0 |
| False positives | 0 | 0 | 0 | 0 | 0 | 0 |
| Unique insights | 1 | 1 | 1 | 0 | 0 | 0 |
| Files reviewed | 1 | 1 | 1 | 1 | 1 | 1 |
| Coverage misses | 0 | 0 | 0 | 0 | 0 | 0 |
| **Totals** | **1I 2S** | **1S** | **1I 3S** | **1S** | **none** | **none** |
| Downgraded | 0 | 0 | 1 (I→S) | 1 (C→S) | 0 | 0 |
| Dropped | 1 | 0 | 0 | 0 | 0 | 0 |


**Reconciliation.** Gemini I1 (context asymmetry, graded Important) and Qwen C1 (same observation, graded Critical) both consolidated as S2 and downgraded to Suggestion — the hang is real but bounded (single-goroutine, not cross-goroutine deadlock). Gemini I2 / Claude S2 kept as I1 at Important — shutdown races matter. Claude S4 (pre-existing log wording) dropped — cosmetic and pre-existing.

The local trio added no findings the cloud trio missed. Qwen converged on S2; Gemma and GLM produced nothing actionable. On this code shape, the local agents are at best redundant and at worst (GLM) unable to complete a structured review under `--variant high`.

---

## Appendix: Per-Agent Reviews

### [Claude]

I have enough context to write the review. The fix is small and targeted, but I want to verify one more thing about how the cleanup path interacts with the new lock.

Now I have a complete picture and will write the review.

# Code Review: `hostagent: stop destroying ga.sock on guest-agent reconnect`

## Header

| | |
|---|---|
| Branch | `fix/ga-sock-reconnect` (1 commit) |
| Files changed | 1 (`pkg/hostagent/hostagent.go`) |
| Issue | lima-vm/lima#2227 |
| Verdict | Approve with optional follow-ups |

## Executive Summary

The change replaces two direct `forwardSSH(verbForward)` calls in the guest-agent reconnect loop with a new helper `reForwardGuestAgentSock` that first issues a best-effort `ssh -O cancel` and then `ssh -O forward`, serialized by a new `gaSockForwardMu`. The diagnosis in the commit message is convincing and the fix is minimal and correct; the residual issues are quality-of-life concerns (log noise on the no-op first call, a brief startup window where the two goroutines still chain a cancel→forward dance back-to-back).

## Findings

### Critical Issues

None.

### Important Issues

None.

### Suggestions

**S1. First-tick `verbCancel` always logs `Stopping forwarding ...` with nothing to stop** — `pkg/hostagent/hostagent.go:984`, `pkg/hostagent/hostagent.go:1036-1042`

```go
// reForwardGuestAgentSock:
_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

`forwardSSH(..., verbCancel, false)` logs `Stopping forwarding %q (guest) to %q (host)` at `Infof` and `defer`s an `os.RemoveAll(local)` unconditionally before it runs `ssh -O cancel`. The very first call to `reForwardGuestAgentSock` on a fresh VM has no prior forward to cancel — but the user still sees a `Stopping forwarding …` line (and the subsequent `Failed to clean up "…/ga.sock" after stopping forwarding` warn that some OSes emit when `RemoveAll` of a missing parent directory or path is racy is suppressed only because `os.RemoveAll` returns nil for non-existent paths). On a typical successful boot the operator now sees the misleading pair:

```
INFO  Stopping forwarding "/run/lima-guestagent.sock" (guest) to "…/ga.sock" (host)
INFO  Forwarding "/run/lima-guestagent.sock" (guest) to "…/ga.sock" (host)
```

Same pattern repeats on every reconnect tick (every 10 s) while the guest agent is down. Functionally harmless, but it looks like the code is fighting itself and complicates log scraping.

Fix: lower the log level of the `verbCancel` info line in `forwardSSH`, or guard `reForwardGuestAgentSock` so it skips the cancel the very first time per process (e.g., a small `bool` next to `gaSockForwardMu`). The smallest change is downgrading the unix-socket cancel log to `Debugf`:

```diff
 } else {
-    logrus.Infof("Stopping forwarding %q (guest) to %q (host)", remote, local)
+    logrus.Debugf("Stopping forwarding %q (guest) to %q (host)", remote, local)
     defer func() {
         if err := os.RemoveAll(local); err != nil {
             logrus.WithError(err).Warnf("Failed to clean up %q (host) after stopping forwarding", local)
         }
     }()
 }
```

That would also quiet the analogous noise from the existing cleanup callsite at line 728.

**S2. `gaSockForwardMu` does not cover the registered cleanup that also mutates `localUnix`** — `pkg/hostagent/hostagent.go:80-84`, `pkg/hostagent/hostagent.go:714-733`

```go
// gaSockForwardMu serializes (re-)establishment of the SSH forward for
// the guest-agent unix socket. The reconnect loop in watchGuestAgentEvents
// and the inotify goroutine both touch the same local socket path; without
// this lock they can race on os.RemoveAll/bind and leave ga.sock missing.
gaSockForwardMu sync.Mutex
```

The cleanup closure registered at line 714 calls `forwardSSH(context.Background(), …, localUnix, remoteUnix, verbCancel, false)` at line 728 without acquiring `gaSockForwardMu`. `close()` (line 682) runs cleanups *before* `cancelHA()` (line 523), so for the window between `close()` starting and `ctx` being observed in the reconnect loop, the cleanup's `verbCancel` (with its deferred `os.RemoveAll`) can race with a live `reForwardGuestAgentSock`. This is pre-existing and limited to shutdown, but the doc comment for the new mutex implies it serializes *all* access to that path. Either the comment should be tightened, or the cleanup closure should take the same lock.

Suggested doc tweak if you'd prefer to leave the behavior alone:

```diff
-// gaSockForwardMu serializes (re-)establishment of the SSH forward for
-// the guest-agent unix socket. The reconnect loop in watchGuestAgentEvents
-// and the inotify goroutine both touch the same local socket path; without
-// this lock they can race on os.RemoveAll/bind and leave ga.sock missing.
+// gaSockForwardMu serializes (re-)establishment of the SSH forward for
+// the guest-agent unix socket between the watchGuestAgentEvents reconnect
+// loop and the inotify setup goroutine. The shutdown cleanup also touches
+// the same path; it intentionally does not take this lock because it runs
+// once and the live goroutines exit on ctx cancellation soon after.
```

**S3. `verbCancel` uses `context.Background()`; `verbForward` uses the live `ctx`** — `pkg/hostagent/hostagent.go:984-985`

```go
_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
if err := forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbForward, false); err != nil {
```

The split mirrors the shutdown cleanup at line 728, but the reasoning the cleanup gives (`// using ctx.Background() because ctx has already been cancelled`, line 721) does not apply here — `reForwardGuestAgentSock` runs from the live reconnect loop with a non-cancelled `ctx`. The asymmetry causes a small foot-gun: if `ctx` is cancelled while the helper is holding `gaSockForwardMu`, the `verbCancel`'s `exec.CommandContext` will not be interrupted but the `verbForward`'s will, so the helper tears the forward down without re-establishing it. The next call comes from the cleanup, which is the desired behavior, so this isn't a regression — but a short comment explaining the choice would prevent a future reader from "fixing" the inconsistency.

Fix: either pass `ctx` to both (and accept that the cancel may be skipped on shutdown), or add a short rationale comment:

```diff
-    sshAddress, sshPort := a.sshAddressPort()
-    // Best-effort teardown of any prior forward registered with the
-    // ControlMaster. Errors are expected (e.g. on the very first call when
-    // no forward exists yet) and intentionally ignored.
-    _ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
+    sshAddress, sshPort := a.sshAddressPort()
+    // Best-effort teardown of any prior forward registered with the
+    // ControlMaster. Uses context.Background so an in-flight cancel still
+    // completes if ctx is cancelled mid-call (the subsequent verbForward
+    // will then fail fast on the cancelled ctx, which is the right thing).
+    // Errors are expected (e.g. on the very first call when no forward
+    // exists yet) and intentionally ignored.
+    _ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

**S4. `forwardSSH` log line says `(guest) to (host)` for an operation that actually unbinds the host listener** — `pkg/hostagent/hostagent.go:1036`

Pre-existing wording, but worth noting because the new code calls `verbCancel` much more often. `Stopping forwarding "/run/lima-guestagent.sock" (guest) to "…/ga.sock" (host)` reads as "the guest is no longer forwarding to the host," which gets the directionality wrong — the host had a listener on `…/ga.sock` that proxied to the guest's socket. Not actionable for this PR; flag for the operator-facing log polish someone may eventually do.

## Design Observations

### Concerns

**Inotify goroutine and main loop both racing the helper at startup is structural.** *(future)*
The inotify goroutine calls `reForwardGuestAgentSock` exactly once at line 742 — at startup, when the main loop is also entering its first iteration at line 777 and will call it as well. The mutex prevents the two from corrupting each other, but the second caller still performs a full cancel→forward dance against the listener the first caller just installed. Dialers that hit `ga.sock` during the second caller's `verbCancel`-defer-`RemoveAll` window see `ENOENT`.

A cleaner shape would be to have the inotify goroutine wait on `a.guestAgentAliveCh` (closed by `processGuestAgentEvents` at line 894 once the main loop has the forward + client up) before doing anything socket-related. The forward setup then has a single owner (the main loop), `gaSockForwardMu` becomes defense-in-depth, and the brief startup unavailability window disappears. Out of scope for a focused bugfix PR.

### Strengths

- The commit message includes a deterministic reproducer (`limactl shell … sudo systemctl restart lima-guestagent.service; sleep 12; ls …/ga.sock`), which is exactly what is needed for an SSH-bound bug that has no automated test.
- The new helper's docstring spells out *why* the prior code corrupted the socket (the duplicate-registration → failure-branch `RemoveAll` sequence), so future readers do not have to re-derive the failure mode.
- Both reconnect callsites are routed through the same helper rather than copy-pasting the cancel+forward pair, which is the right way to centralize the locking contract.

## Testing Assessment

No tests were added or modified. Given the bug requires real SSH, a real ControlMaster, and a real guest agent that can be restarted, a Go unit test would not catch it. The deterministic shell reproducer in the commit message is reasonable evidence; a BATS test that restarts `lima-guestagent.service` and verifies `ga.sock` is present after a sleep would be a stronger guard against regression, but the existing BATS suite does not seem to exercise this surface and adding the harness is a larger change.

Untested scenarios ranked by risk:

1. Guest-agent restart loop on a long-running instance — exactly the bug being fixed; no automated coverage.
2. VM reboot recovery — same code path; not exercised by unit tests.
3. Windows (WSL2 driver returns true from `ForwardGuestAgent`) — `ssh -O cancel` reliability depends on which SSH toolchain is installed; not exercised here.

## Documentation Assessment

The new field comment (lines 80–84) and the new helper docstring (lines 956–976) are good. See S2 for one small accuracy gap around what the mutex does not cover.

## Commit Structure

Clean. One commit, focused on one fix, DCO sign-off present, message explains the failure mode and the fix.

## Acknowledged Limitations

None recorded in code or commit message.

## Coverage Summary

| File | Status |
|---|---|
| `pkg/hostagent/hostagent.go` | S1, S2, S3, S4 |
### [Codex]

### Executive Summary

This change routes guest-agent `ga.sock` recovery through a serialized cancel-then-forward helper so stale SSH ControlMaster forwards are released before rebinding the local Unix socket. I found no critical or important regressions; the remaining concern is a small context-propagation issue already raised in PR feedback.

### Findings

#### Critical Issues

None.

#### Important Issues

None.

#### Suggestions

S1. **Propagate the reconnect context into the cancel command** — `pkg/hostagent/hostagent.go:984` *(suggestion, regression)*

```go
	// no forward exists yet) and intentionally ignored.
	_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
	if err := forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbForward, false); err != nil {
		logrus.WithError(err).Warn("failed to (re-)establish forward for the guest agent socket; will retry")
	}
```

`reForwardGuestAgentSock` starts the best-effort cancel with `context.Background()` at line 984, while its live reconnect callers pass the host-agent context at lines 742 and 777. Because `forwardSSH` uses `exec.CommandContext` at line 1047, detaching the cancel command from `ctx` means shutdown cancellation cannot interrupt a stuck `ssh -O cancel`; while that command is stuck, `gaSockForwardMu` remains held from line 978 and the other reconnect path cannot make progress. This differs from the cleanup path at line 721, where the comment explicitly explains why `context.Background()` is needed after the normal context has already been cancelled.

Fix: use the caller context for this live reconnect operation. If the intent is also to bound reconnect latency, derive a short timeout from `ctx`; the key is not to detach from host-agent cancellation.

```diff
 	// Best-effort teardown of any prior forward registered with the
 	// ControlMaster. Errors are expected (e.g. on the very first call when
 	// no forward exists yet) and intentionally ignored.
-	_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
+	_ = forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
 	if err := forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbForward, false); err != nil {
 		logrus.WithError(err).Warn("failed to (re-)establish forward for the guest agent socket; will retry")
 	}
```

### Design Observations

**Strengths** — The cancel-before-forward sequence addresses the actual ControlMaster state that made the missing `ga.sock` condition persistent, rather than only preserving the filesystem socket path.

**Strengths** — The dedicated `gaSockForwardMu` is scoped to the guest-agent socket forward and avoids broadening synchronization around unrelated SSH socket forwarding.

### Testing Assessment

`go test ./pkg/hostagent` passes.

Untested scenarios, ranked by risk:

1. Guest agent is stopped long enough to force a reconnect tick, leaving the old ControlMaster forward registered while the local `ga.sock` path has been removed.
2. Main guest-agent event loop and the mount-inotify goroutine both attempt `reForwardGuestAgentSock` at the same time.
3. Host-agent context is cancelled while `ssh -O cancel` or `ssh -O forward` is in progress.

### Documentation Assessment

No user-facing documentation update appears necessary. The new helper comment documents the ControlMaster failure mode and the synchronization reason clearly.

### Acknowledged Limitations

- **Code comment**: `// TODO: use vSock (when QEMU for macOS gets support for vSock)` — `pkg/hostagent/hostagent.go:697`. This is unchanged and does not affect the SSH-forwarding fix.
- **Code comment**: `// Best-effort teardown of any prior forward registered with the // ControlMaster. Errors are expected ... and intentionally ignored.` — `pkg/hostagent/hostagent.go:981`. The change intentionally tolerates cancel failures and relies on retry behavior; S1 is about context lifetime, not the decision to ignore cancel errors.

### Unresolved Feedback

- https://github.com/lima-vm/lima/pull/4911#discussion_r3244931774 — Reviewer questioned why the new cancel call uses `context.Background()` instead of `ctx`. Still actionable; this is covered by S1.

### Coverage Summary

- `pkg/hostagent/hostagent.go` — S1.

### [Gemini]

### Executive Summary

This PR addresses issue #2227 by modifying the host agent's reconnect loop to cleanly cancel the existing SSH forward (`verbCancel`) before attempting to re-establish it (`verbForward`), and it introduces a mutex (`gaSockForwardMu`) to prevent concurrent file system races between the inotify and reconnect goroutines. The core logic successfully resolves the socket destruction bug. However, the implementation introduces an uncancellable context during the live reconnect loop that can cause hangs, misses a race condition during shutdown cleanup, and exhibits a minor double-initialization flaw at startup.

### Findings

#### Critical Issues

None.

#### Important Issues

I1. **Uncancellable context in reForwardGuestAgentSock causes reconnect loop to hang** — `pkg/hostagent/hostagent.go:984` (important, regression)

```go
	// Best-effort teardown of any prior forward registered with the
	// ControlMaster. Errors are expected (e.g. on the very first call when
	// no forward exists yet) and intentionally ignored.
	_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

The fix introduces a call to `forwardSSH` with `verbCancel` using `context.Background()`. If the SSH ControlMaster process is stalled (e.g., due to a broken kernel network state or hung VM), `ssh -O cancel` will block indefinitely. Because `context.Background()` cannot be cancelled, the goroutine holding `gaSockForwardMu` will hang forever. This permanently breaks the reconnect loop, prevents other goroutines from using the forward mutex, and prevents the host agent from shutting down cleanly. The author likely copied this from the shutdown cleanup block, but in a live reconnect loop, failing to use the provided `ctx` creates a deadlock vector.

Fix: Pass `ctx` to the `verbCancel` call instead of `context.Background()`. 

```diff
-	_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
+	_ = forwardSSH(ctx, a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

I2. **Missing lock in cleanUp leaves ga.sock exposed to teardown races** — `pkg/hostagent/hostagent.go:727-731` (important, gap)

```go
		if a.driver.ForwardGuestAgent() {
			if err := forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false); err != nil {
				errs = append(errs, err)
			}
		}
```

The PR introduces `gaSockForwardMu` to serialize `os.RemoveAll/bind` operations on `ga.sock` between the reconnect loop and the inotify goroutine. However, the teardown logic in `cleanUp` still calls `forwardSSH(..., verbCancel)` on `localUnix` *without* acquiring `gaSockForwardMu`. Since `cleanUp` executes concurrently with the `watchGuestAgentEvents` loops during shutdown, `cleanUp` can invoke `ssh -O cancel` and `os.RemoveAll` on the socket while `reForwardGuestAgentSock` is halfway through recreating it. This defeats the purpose of the mutex and risks leaving unexpected state on the filesystem if the processes interleave.

Fix: Acquire `gaSockForwardMu` in the `cleanUp` function before calling `forwardSSH` for the guest agent socket.

```diff
 		if a.driver.ForwardGuestAgent() {
+			a.gaSockForwardMu.Lock()
 			if err := forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false); err != nil {
 				errs = append(errs, err)
 			}
+			a.gaSockForwardMu.Unlock()
 		}
```

#### Suggestions

S1. **Redundant forward setup in inotify goroutine causes double-initialization** — `pkg/hostagent/hostagent.go:739-745` (suggestion, gap)

```go
		client := a.getClient()
		if client == nil || !isGuestAgentSocketAccessible(ctx, client) {
			if a.driver.ForwardGuestAgent() {
				a.reForwardGuestAgentSock(ctx, localUnix, remoteUnix)
			}
		}
```

At startup, both the main `watchGuestAgentEvents` loop and the spawned `inotify` goroutine concurrently evaluate `isGuestAgentSocketAccessible(ctx, client)`. Since the guest agent is not running yet, both evaluate to false and call `reForwardGuestAgentSock`. The new `gaSockForwardMu` serializes them, resulting in the forward being established by the first goroutine, immediately torn down, and then redundantly re-established by the second goroutine. 

Fix: Remove the `reForwardGuestAgentSock` block from the `inotify` goroutine. The main loop already handles establishing and repairing the forward reliably; the inotify goroutine should simply rely on the main loop's setup and retry `startInotify` until it succeeds.

```diff
 	go func() {
 		if a.instConfig.MountInotify == nil || !*a.instConfig.MountInotify {
 			return
 		}
-		client := a.getClient()
-		if client == nil || !isGuestAgentSocketAccessible(ctx, client) {
-			if a.driver.ForwardGuestAgent() {
-				a.reForwardGuestAgentSock(ctx, localUnix, remoteUnix)
-			}
-		}
 		// Re-spawn startInotify when its gRPC stream dies (typically because
```

### Design Observations

**Strengths**
- The two-step approach (`verbCancel` then `verbForward`) cleanly addresses the multiplexer's duplicate registration failure without requiring deeper surgery on the SSH ControlMaster lifecycle. 
- The comment block added to `reForwardGuestAgentSock` is exceptionally clear and well-documented.

### Testing Assessment

1. **Guest Agent Reconnect Flow**: The core fix modifies SSH multiplexer state and relies on file system operations during concurrent goroutine execution. While the manual testing on #2227 proves functionality, adding a BATS integration test that explicitly stops the guest agent (`sudo systemctl stop lima-guestagent`), waits 15 seconds, starts it, and asserts that host↔guest operations (e.g. inotify propagation) recover successfully would definitively prevent future regressions in this lifecycle.

### Documentation Assessment

The docstring added above `reForwardGuestAgentSock` provides excellent context on the history of the bug, the mechanics of the duplicate registration failure, and the intent of the fix. 

### Coverage Summary

- `pkg/hostagent/hostagent.go` — I1, I2, S1


### [Qwen]


# Executive Summary

This change fixes a race condition that permanently breaks the guest agent socket forward (ga.sock) on reconnect. The previous code called `forwardSSH(verbForward)` directly, which unlinks the socket and conflicts with the SSH ControlMaster's existing registration. The new `reForwardGuestAgentSock` method first cancels any existing forward (best-effort), then establishes the new forward, and serializes all such operations with a mutex to prevent races between the reconnect loop and inotify goroutine.

**Key finding:** The `verbCancel` call uses `context.Background()` instead of the provided `ctx`, which could cause the cancel to block or timeout if `ctx` is cancelled, potentially delaying the subsequent forward operation.

---

## Critical Issues — must fix before merge

**C1. Context used for verbCancel is hardcoded to Background instead of using the function parameter ctx** — `pkg/hostagent/hostagent.go:972-976`

```go
// Best-effort teardown of any prior forward registered with the
// ControlMaster. Errors are expected (e.g. on the very first call when
// no forward exists yet) and intentionally ignored.
_ = forwardSSH(context.Background(), a.sshConfig, sshAddress, sshPort, localUnix, remoteUnix, verbCancel, false)
```

The function accepts a `ctx` parameter but discards it for the `verbCancel` call, using `context.Background()` instead. If the caller's context is cancelled or has a deadline, the cancel operation will ignore it and may block or wait unnecessarily, delaying the subsequent `verbForward` call. The comment says errors are expected and ignored, but doesn't explain why the context is ignored when there's no reason to believe the context is unsuitable.

Fix: Use `ctx` instead of `context.Background()` to honor the caller's timeout and cancellation semantics.

---

## Important Issues — should fix

None.

---

## Suggestions — nice to have

**S1. Consider documenting that verbCancel errors are expected on first call** — `pkg/hostagent/hostagent.go:972-973`

```go
// Best-effort teardown of any prior forward registered with the
// ControlMaster. Errors are expected (e.g. on the very first call when
// no forward exists yet) and intentionally ignored.
```

The comment already explains why errors are ignored. Consider also noting that `verbCancel` may fail if no prior forward exists (which is normal on first call) versus failing because the ControlMaster connection is unhealthy (which would warrant logging).

---

## Design Observations

**Strengths**
- The mutex-based serialization (`gaSockForwardMu`) correctly prevents races between the reconnect loop in `watchGuestAgentEvents` and the inotify setup goroutine touching the same local socket path.
- The two-part fix (cancel-then-forward) directly addresses the root cause identified in the comment: duplicate ControlMaster registrations causing ssh to unlink the socket twice.

---

## Testing Assessment

Untested scenarios:
1. Call to `reForwardGuestAgentSock` when `ctx` is cancelled or has a deadline—does `verbCancel` with `context.Background()` complete successfully while ignoring the cancelled context, or does it hang until the underlying SSH command times out?
2. Rapid consecutive calls to `reForwardGuestAgentSock`—does the mutex ensure the first cancel finishes before the second cancel begins, preventing a scenario where the second forward sees an inconsistent state?
3. `verbCancel` succeeds but `verbForward` fails—what is the state of the socket file and ControlMaster registration? Does the next retry succeed cleanly, or is cleanup required?

---

## Documentation Assessment

The `reForwardGuestAgentSock` docstring (lines 960-970) is comprehensive and explains the bug, the two-part fix, and the mutex. The only update needed is to harmonize the comment at line 972-973 to reflect that `context.Background()` is intentionally used (not a mistake) because the cancel is best-effort and should not be cancelled itself.

---

## Coverage Summary

- **pkg/hostagent/hostagent.go** — C1, S1
### [Gemma]


### Executive Summary

This change introduces a synchronized mechanism to (re-)establish the SSH local forward for the guest-agent socket. By ensuring a `verbCancel` operation precedes `verbForward` and serializing these calls via a new mutex (`gaSockForwardMu`), the fix prevents a race condition and avoids a known issue where the local socket file (`ga.sock`) could be permanently deleted due to duplicate registrations in the SSH ControlMaster.

### Findings

#### Critical Issues
None.

#### Important Issues
None.

#### Suggestions
None.

### Design Observations

**Strengths**
- The implementation of `reForwardGuestAgentSock` correctly addresses the root cause by explicitly tearing down existing forwards in the ControlMaster before attempting a new bind.
- The use of a dedicated mutex (`gaSockForwardMu`) effectively isolates the socket-path manipulation from other `HostAgent` state transitions, avoiding unnecessary contention on the global `clientMu`.
- The detailed docstring for `reForwardGuestAgentSock` provides excellent context and references the specific failure mode (#2227), making the "why" behind the fix clear for future maintainers.

### Testing Assessment

The change is primarily a fix for a race condition and a side-effect of SSH ControlMaster behavior. While it is logically sound, the following scenarios are the most risky:
1. **Concurrent Reconnects**: Verifying that the system remains stable when both the `watchGuestAgentEvents` main loop and the inotify goroutine trigger a reconnect simultaneously.
2. **ControlMaster Failures**: Ensuring that if the `verbCancel` operation fails (e.g., the master is already dead), the subsequent `verbForward` can still proceed effectively.

### Documentation Assessment

The code is well-documented. The added comments in the `HostAgent` struct and the `reForwardGuestAgentSock` method clearly explain the reasoning and the twofold nature of the fix.

### Coverage Summary

- `pkg/hostagent/hostagent.go`: Reviewed, no issues.
### [GLM]



I'll perform a thorough review of this change. Let me start by reading the complete affected file to understand the context.
