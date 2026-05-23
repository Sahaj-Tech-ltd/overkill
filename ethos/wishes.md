1 advanced chat things, like bot replies to chats, a reply from user loads that msg back into memeory, hey i frgot what we talking abt, tag the msg pls, and i;'' dive into that session / context. 
2 the agent knows how far or close it is to compaction, so if its near say 48%, and user wants it to go do a massive task, it says hold on lemme compact so im fresh , take appropirate setps likemaking a plan, updating stuff, etc so that nothing worth is essentially lost. 
3 defaults , 1 default per task, if it takes the agent more than the set variable time, given complexity of task, it goes up or down, code hello world- 1 min timeout, it api fails or whatveer, it intterupts. 
4 alsways runs commands that end in echo something back, so our system always knows when  command is done running. 

5 testing philosophy — a layer not a list.

tests should hunt for failure, not confirm success. the old way is write code, write tests that prove the code works, ship. that gives you confidence on good days. we want confidence on bad days.

every project needs a testing layer built like this:

FIRST: machine-checked auth guard. don't curate a list of "protected routes" by hand. pull every registered route from the router and assert every single one returns 401 with no token. if a new route gets added without auth, the test fails automatically. no human has to remember to add it.

SECOND: tests written to fail on current code and pass when the bug is fixed. if you found a bug, write the test before touching the code. the test goes red, you fix the bug, the test goes green. that test now lives forever as a regression guard. not a comment in a doc, not a note in slack — a failing test.

THIRD: negative cases for every endpoint. for every "POST /water with 250ml succeeds" test there must be a "POST /water with -1ml returns 400" test. for every valid meal_type test there must be an invalid one. boundaries in both directions. if validation is missing the test documents it explicitly with a console.warn or t.Errorf citing the bug id.

FOURTH: concurrency tests. fire N goroutines at the same endpoint simultaneously. if two concurrent requests create duplicate rows, the test finds it. if a race condition drops writes, the test finds it. if concurrent requests cause a panic, the test finds it. these are the bugs that only appear in production under load and never in dev.

FIFTH: error shape consistency. every error response in the whole API must be JSON with an "error" field. write one test that sends bad input to every error path and asserts the shape. html errors, empty bodies, plain text — all fail. this enforces a contract across the whole surface.

SIXTH: bug regression tests. every bug that gets documented in bugs.md gets a test filed alongside it, in the same commit. the test name contains the bug id (AH1, NH2, etc). when someone fixes the bug they know exactly which test to run to verify. when someone accidentally reintroduces the bug in 6 months, CI catches it.

the ethos: tests are a second reader of your code. they should ask the uncomfortable questions — what happens when this is null, what happens when two users do this at the same time, what happens when the db is wrong. happy path tests are marketing. negative tests are engineering.

6 user is source of truth — model should not correct the user's premise, it should act on it.

if the user asks about something the model doesn't know (iphone 17, a library released after training cutoff, a person, a price, anything that could have changed) the model must not assume the user is wrong and redirect them to what it does know. that's the most annoying thing a model can do. the user said 17. they meant 17.

the right behavior: treat the user's claim as fact, then go get the answer. run a web search. fetch the docs. look it up. if the search comes back empty or contradicts them, surface that — but surface it as new information, not as "actually you meant X." the user's words are the spec. the model's training data is a prior, not an authority.

the wrong behavior: "i think you might mean the iphone 16, as i don't have information about an iphone 17." that's the model prioritizing its own knowledge boundary over the user's intent. it's condescending and it wastes the user's time.

rule: when the user states something as fact that the model can't verify from training — search first, correct never. if after searching it's genuinely wrong, say "i searched and found X — does that match what you're looking for?" not "you must have meant X."

7 session teleport — hand off a live local session to a remote runner with state intact.

today we have session sync (S3 / git / file) and gateways that can drive a session from a phone. what we don't have is the primitive: take a live TUI session — history, pending tool calls, approvals, working dir context — snapshot it, ship it to a daemon-mode overkill on a VPS, and have the chat bridge subscribe to the new runner so the agent keeps working after the laptop closes. sync moves bytes; teleport moves execution.

shape: `/teleport <target>` snapshots BadgerDB state + the in-flight tool call queue, rsyncs to target, daemon resumes from the snapshot. on the local side, the TUI detaches but can `/attach <session-id>` later from anywhere — laptop, mobile app, gateway. the daemon is the source of truth between detach and re-attach.

this is the missing primitive behind the mobile story. without it, "remote control via app" is just chat-over-ssh; with it, the agent is genuinely portable across surfaces and survives the user going offline.
