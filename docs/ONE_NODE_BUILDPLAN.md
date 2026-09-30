# Build plan: one native macOS node for the voice stack

**Goal:** Run the LLM, Whisper, Kokoro, and voice agent as four Pods on one
macOS virtual node backed by one `macd` socket. Keep the dedicated Kind
cluster available for the voice stack.

| Step | Status | Acceptance check |
| --- | --- | --- |
| 1. Record live state and stop voice workloads | Done | Saved the LLM model mount; all six voice Deployments and Pods were removed, and four voice daemon/adapter pairs stopped. |
| 2. Support multiple native Pods | Done | One `mackube` accepted four concurrent Pods and advertised capacity for four; all four were Running on `macnative`. |
| 3. Put all images behind one `macd` | Done | One Docker socket resolved all four image tags and launched all four containers concurrently. |
| 4. Update placement and operator docs | Done | Four native manifests select the common macOS node without per-service slot labels; docs show one daemon and one adapter. |
| 5. Redeploy and verify | Voice redeployment paused at user request | The one-node deployment ran all six Pods; LLM, Kokoro, and web health checks passed. A separate tiny native Pod verified live Stern logging. All six voice Deployments and Pods remain stopped. |

During verification, Stern exposed that `kubectl logs` with `tailLines`,
`follow=true`, its default 48-hour `sinceSeconds`, and `timestamps=true` was
rejected. The adapter now passes these options to `macd`, which filters
retained logs by time, selects the tail, adds timestamps, and then streams new
frames. Regression tests cover the adapter request and daemon stream. The
temporary tiny Go Pod confirmed that Stern with `--tail 1` showed only the
last retained line, then followed a new request. Stern with default flags
showed existing lines and followed another new request. The probe Pod, node,
and processes were removed afterward; the voice deployment stays stopped.

The dedicated `livekit` Kind cluster stays in place. An unrelated `macd`
instance serving the separate llama socket is outside this migration.
