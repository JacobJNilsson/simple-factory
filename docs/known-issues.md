# Known issues

This file records open problems that sfy has chosen not to fix in its core. Each entry says what was tried and why it was rejected, so nobody tries the same thing again.

sfy stays simple. Its core holds tasks, states, the review loop, and agent messages. Terminals plug in through the template set in `config.toml`. Terminal details stay out of Go code.

## Notes typed into the operator's draft

### Problem

sfy types a note into the main agent's terminal with the multiplexer's Send template. The operator may be typing a message in that terminal at that moment. Then the note and the unsent keystrokes reach the agent as one message, for example "So thatNew message. Run: sfy inbox". The rest of the operator's text is lost. This happened three times on 2026-11-15.

### Cause

Send types into the input line. No supported terminal tool tells sfy what the input line holds. Herdr's `hc send` holds a send while the operator types, for `draft_wait` seconds (default 60). Then it types anyway.

### Rejected options

- **Save the draft, send the note, type the draft back.** Task t25 tried this and was cancelled. `hc` has no API to read or set the input line. Reading the line from the screen is not reliable, and a misread loses the operator's text.
- **A draft API in `hc`.** This is a change to Herdr, which is outside this project.
- **A long `draft_wait`, such as an hour.** Notes cannot wait that long.
- **Rex as the terminal.** Rex has no method that reads the input line either. This was tested on 2026-11-16. See `docs/rex.md`.
- **sfy holds the agents' terminals itself.** sfy would pass keystrokes through and hold notes while a draft exists. That makes sfy a terminal broker and goes against the small core.
- **Headless workers with `claude -p`.** They use the prompt cache worse and burn more tokens than an interactive session.
- **sfy shows or switches between worker screens in one terminal.** Nobody needs to see the workers.

### Status

Open. No fix is chosen. A fix belongs in the terminal tool or in config, not in sfy's core.
