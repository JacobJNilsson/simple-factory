# Running sfy on Herdr

sfy has no Herdr code. Herdr plugs in through a template set in `~/.sfy/config.toml`.

This note holds a working set. It shows the main agent and hides every worker and reviewer.

## Example config

Add this to `~/.sfy/config.toml`:

```toml
multiplexer = "herdr"

[multiplexers.herdr]
start = '''
hc run --bg --name {name} --cwd {dir} --no-parent --no-notify -- {command}
'''
send = '''
hc send {name} -- {text}
'''
# A finished terminal stays listed and hc get exits 0 for it, so alive checks the state.
# Exit 1 means missing or finished. Any other hc failure exits 2, so alive reports an error and not "gone".
alive = '''
out=$(hc get {name} --json 2>&1); rc=$?
case $out in *'"kind":"not_found"'*) exit 1;; esac
if [ $rc -ne 0 ]; then printf '%s\n' "$out" >&2; exit 2; fi
case $out in *'"state":"live"'*) exit 0;; esac
exit 1
'''
stop = '''
hc kill {name}
'''
# An empty show keeps workers and reviewers hidden. Their terminals run in the background.
show = ""
# A background terminal has no pane. herdr-app needs its address, which hc get prints in the JSON.
show_main = '''
out=$(hc get {name} --json) || exit 1
addr=$(printf '%s' "$out" | sed -n 's/.*"address":"\([^"]*\)".*/\1/p')
[ -n "$addr" ] || { echo "no address for {name}" >&2; exit 1; }
# The Herdr app learns a new terminal a moment after hc run returns, so keep retries until it knows it.
i=0
while :; do
  res=$(herdr-app do terminal.keep --terminal "$addr" 2>&1) && exit 0
  i=$((i+1))
  [ $i -lt 10 ] || break
  sleep "${SFY_SHOW_WAIT:-0.5}"
done
printf '%s\n' "$res" >&2
exit 1
'''
```

## Notes on the example

- sfy runs `show_main` for the main agent and `show` for workers and reviewers. With `show = ""`, a worker gets no Show.
- To show every agent, put the `show_main` script in `show` and remove `show_main`.
- A test loads this example and runs its templates against stub commands, so the file stays valid. See `herdrSet` in `internal/mux/mux_test.go`.
- A worker that stops at the provider's trust dialog needs the operator. Open that agent's terminal in Herdr and accept the dialog.
