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
| 5. Redeploy and verify | Paused at user request | The one-node deployment ran all six Pods; LLM, Kokoro, and web health checks passed. All six Deployments and Pods are now stopped. Live Stern verification awaits redeployment. |

During verification, Stern exposed that `kubectl logs` with `tailLines` and
`follow=true` was rejected. The adapter now passes both options to `macd`,
which selects the retained tail before continuing to stream new frames.
Regression tests cover the adapter request and the daemon stream. The voice
deployment must stay stopped until the user asks to redeploy it.

The dedicated `livekit` Kind cluster stays in place. An unrelated `macd`
instance serving the separate llama socket is outside this migration.
