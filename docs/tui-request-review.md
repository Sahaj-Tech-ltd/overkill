# Terminal request review

In Agents, inspect a blocked request and press `v` to open Usage. An enabled
agent that both participates in the request and owns its usage budget sees
`b:review budget`. The server remains the authority for permission and replay
eligibility; a visible action does not guarantee that the review will pass.

Press `b` to capture the current request and usage revisions. Enter explicit
token and USD limits (`0` means unlimited), then a nonempty review note of at
most 2048 UTF-8 bytes. Use Enter to advance through fields, or the left/right
arrows to revisit them. At confirmation, press `c` to acknowledge that replay
may repeat prior effects, then Enter to submit. Editing any field clears that
confirmation. No model-facing tool grants this authority.

The UI does not erase usage counters, unknown evidence, pending executions,
prerequisites, or contract pins. The backend checks these together with leases,
deadlines, attempt limits, and both captured revisions before atomically
adjusting the budget and requeueing the request.

After submission, the body and fresh review UUID are locked. A connection
failure or revision conflict does not silently create a new review. Enter
manually retries the exact same body and UUID; the UI never retries
automatically. Escape closes the form. Refresh before beginning another
review, and inspect existing effects before authorizing replay. Switching
sessions aborts the old request and prevents its late response from changing
the current form.
