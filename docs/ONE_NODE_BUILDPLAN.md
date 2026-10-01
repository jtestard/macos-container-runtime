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
| 5. Redeploy and verify | Done | All six Deployments are Available. Four native Pods run on the one Ready `macnative` node; LiveKit and web run in Kind. LLM and Kokoro health checks return HTTP 200, web `/config` responds, Stern tails Kokoro, and the agent reports `registered worker`. |

During verification, Stern exposed that `kubectl logs` with `tailLines`,
`follow=true`, its default 48-hour `sinceSeconds`, and `timestamps=true` was
rejected. The adapter now passes these options to `macd`, which filters
retained logs by time, selects the tail, adds timestamps, and then streams new
frames. Regression tests cover the adapter request and daemon stream. The
temporary tiny Go Pod confirmed that Stern with `--tail 1` showed only the
last retained line, then followed a new request. Stern with default flags
showed existing lines and followed another new request. The probe Pod, node,
and processes were removed afterward. The voice deployment was restored on
2026-10-01 with one `macd` and one `mackube` process.

The dedicated `livekit` Kind cluster stays in place. The separate llama socket,
when used, is outside this voice-stack migration.
