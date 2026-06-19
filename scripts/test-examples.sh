#!/usr/bin/env bash
#
# scripts/test-examples.sh
#
# Drives playwright-cli through every Sigil example (.sigil + Go) and
# checks observed DOM/runtime state after a few interactions. Reports
# PASS / FAIL per assertion plus an overall summary.
#
# Requires: @playwright/cli installed globally (playwright-cli binary).
#           Chromium installed (`playwright-cli install-browser chromium`).
#           Sigil binary in bin/sigil (run `make build` first).

set -u

PWCLI=/Users/sethlowie/.global-node-packages/bin/playwright-cli
PASS=0
FAIL=0
FAILURES=()
TMP=$(mktemp -d -t sigil-test.XXXXXX)

cleanup() {
  # Kill any servers we spawned.
  pkill -f "bin/sigil run" 2>/dev/null
  pkill -f "go run ./examples/" 2>/dev/null
  # Close all playwright sessions.
  $PWCLI close-all > /dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

# ---------------- helpers ----------------

# expect <label> <got> <want> — record pass/fail.
expect() {
  local label=$1 got=$2 want=$3
  if [ "$got" = "$want" ]; then
    echo "  ✓ $label"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $label: got '$got', want '$want'"
    FAIL=$((FAIL + 1))
    FAILURES+=("$label: got '$got', want '$want'")
  fi
}

# expect_contains <label> <haystack> <needle>
expect_contains() {
  local label=$1 haystack=$2 needle=$3
  if [[ "$haystack" == *"$needle"* ]]; then
    echo "  ✓ $label"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $label: '$needle' not in '$haystack'"
    FAIL=$((FAIL + 1))
    FAILURES+=("$label: '$needle' not in '$haystack'")
  fi
}

# start_sigil <file> <port> — launch sigil run, echo PID, wait briefly.
start_sigil() {
  local file=$1 port=$2
  ./bin/sigil run "$file" -a ":$port" > "$TMP/srv_$port.log" 2>&1 &
  echo $!
  sleep 1
}

# start_go <example-dir> <port> — launch a Go example, echo PID, wait.
start_go() {
  local dir=$1 port=$2
  go run "./$dir" -addr ":$port" > "$TMP/srv_$port.log" 2>&1 &
  echo $!
  sleep 2
}

# pw_eval <session> <js-expr> — eval and return raw result (unquoted,
# inner escape-sequences decoded so JSON.stringify output round-trips
# back to recognizable JSON text).
pw_eval() {
  $PWCLI -s="$1" eval "$2" 2>&1 \
    | grep -A1 '^### Result' | tail -1 \
    | sed -E 's/^"(.*)"$/\1/' \
    | sed 's/\\"/"/g; s/\\\\/\\/g'
}

# pw_open <session> <url> — open the page, wait for sigil runtime to load.
pw_open() {
  $PWCLI -s="$1" open "$2" > /dev/null 2>&1
  pw_eval "$1" 'typeof window.sigil' > /dev/null # blocks until JS evaluates
}

# pw_snap <session> — full raw snapshot to stdout.
pw_snap() { $PWCLI -s="$1" --raw snapshot 2>&1; }

# button_ref <session> <label> — find ref of a button by exact accessible name.
button_ref() {
  pw_snap "$1" | grep "button \"$2\"" | head -1 | grep -oE 'ref=e[0-9]+' | head -1 | cut -d= -f2
}

# textbox_ref <session> — find ref of a textbox.
textbox_ref() {
  pw_snap "$1" | grep -E 'textbox' | head -1 | grep -oE 'ref=e[0-9]+' | head -1 | cut -d= -f2
}

# read_cell <session> <cell-id> — textContent of the bound text node for a cell.
read_cell() {
  pw_eval "$1" "document.querySelector('[data-sigil-bind-text=$2]').textContent"
}

# read_cells_json <session> — full sigil.cells map as JSON string.
read_cells_json() {
  pw_eval "$1" "JSON.stringify(window.sigil.cells)"
}

# session_done <session> <pid> — close playwright session + kill server.
session_done() {
  $PWCLI -s="$1" close > /dev/null 2>&1 || true
  kill "$2" 2>/dev/null || true
}

# ---------------- tests: sigil examples ----------------

test_sigil_hello() {
  echo "[sigil/hello.sigil]"
  local PID; PID=$(start_sigil examples/sigil/hello.sigil 9101)
  pw_open hello http://localhost:9101/
  local snap; snap=$(pw_snap hello)
  expect_contains "page renders title" "$snap" 'Hello, Sigil'
  expect_contains "page renders text"  "$snap" 'Authored in .sigil'
  session_done hello "$PID"
}

test_sigil_layout() {
  echo "[sigil/layout.sigil]"
  local PID; PID=$(start_sigil examples/sigil/layout.sigil 9102)
  pw_open layout http://localhost:9102/
  local snap; snap=$(pw_snap layout)
  expect_contains "layout heading"  "$snap" 'Layout demo'
  expect_contains "left cell"       "$snap" 'left'
  expect_contains "middle cell"     "$snap" 'middle'
  expect_contains "right cell"      "$snap" 'right'
  session_done layout "$PID"
}

test_sigil_counter() {
  echo "[sigil/counter.sigil]"
  local PID; PID=$(start_sigil examples/sigil/counter.sigil 9103)
  pw_open counter http://localhost:9103/
  expect "initial count" "$(read_cell counter c1)" "0"

  local PLUS RESET MINUS
  PLUS=$(button_ref counter '+')
  RESET=$(button_ref counter 'reset')
  MINUS=$(button_ref counter '-')

  $PWCLI -s=counter click "$PLUS" > /dev/null
  $PWCLI -s=counter click "$PLUS" > /dev/null
  $PWCLI -s=counter click "$PLUS" > /dev/null
  expect "after +++"      "$(read_cell counter c1)" "3"

  $PWCLI -s=counter click "$MINUS" > /dev/null
  expect "after -"        "$(read_cell counter c1)" "2"

  $PWCLI -s=counter click "$RESET" > /dev/null
  expect "after reset"    "$(read_cell counter c1)" "0"

  session_done counter "$PID"
}

test_sigil_echo() {
  echo "[sigil/echo.sigil]"
  local PID; PID=$(start_sigil examples/sigil/echo.sigil 9104)
  pw_open echo http://localhost:9104/
  expect "initial state cell" "$(read_cell echo c1)" "hello, alice"

  local BOB CLAUDE
  BOB=$(button_ref echo 'bob')
  CLAUDE=$(button_ref echo 'claude')

  $PWCLI -s=echo click "$BOB" > /dev/null
  expect "after click bob"    "$(read_cell echo c1)" "hello, bob"

  $PWCLI -s=echo click "$CLAUDE" > /dev/null
  expect "after click claude" "$(read_cell echo c1)" "hello, claude"

  session_done echo "$PID"
}

test_sigil_disclosure() {
  echo "[sigil/disclosure.sigil]"
  local PID; PID=$(start_sigil examples/sigil/disclosure.sigil 9105)
  pw_open disc http://localhost:9105/
  # c1 = expanded, c2 = count
  expect "initial count text" "$(read_cell disc c2)" "count: 0"
  local snap; snap=$(pw_snap disc)
  if [[ "$snap" == *"Hidden content revealed"* ]]; then
    echo "  ✗ hidden text leaked into visible snapshot"
    FAIL=$((FAIL + 1)); FAILURES+=("disclosure: hidden leaked")
  else
    echo "  ✓ hidden text inert before toggle"
    PASS=$((PASS + 1))
  fi

  local TOGGLE PLUS
  TOGGLE=$(button_ref disc 'toggle')
  PLUS=$(button_ref disc '+')

  $PWCLI -s=disc click "$PLUS" > /dev/null
  $PWCLI -s=disc click "$PLUS" > /dev/null
  expect "+ twice updates outer count" "$(read_cell disc c2)" "count: 2"

  $PWCLI -s=disc click "$TOGGLE" > /dev/null
  snap=$(pw_snap disc)
  expect_contains "after toggle on: hidden visible" "$snap" 'Hidden content revealed'
  expect_contains "after toggle on: inner count synced" "$snap" 'count is bound here too: 2'

  $PWCLI -s=disc click "$TOGGLE" > /dev/null
  snap=$(pw_snap disc)
  if [[ "$snap" == *"Hidden content revealed"* ]]; then
    echo "  ✗ hidden text still visible after second toggle"
    FAIL=$((FAIL + 1)); FAILURES+=("disclosure: toggle off failed")
  else
    echo "  ✓ toggle off: hidden re-detached"
    PASS=$((PASS + 1))
  fi

  session_done disc "$PID"
}

test_sigil_todo() {
  echo "[sigil/todo.sigil]"
  local PID; PID=$(start_sigil examples/sigil/todo.sigil 9114)
  pw_open todo http://localhost:9114/

  expect "initial items list null/empty" "$(pw_eval todo 'JSON.stringify(window.sigil.cells.c1 || [])')" "[]"
  expect "initial draft empty"           "$(pw_eval todo 'window.sigil.cells.c2')" ""

  # Type + Add — multi-statement handler appends draft (via $cell.X
  # sentinel) and clears it. Each append produces FLAT dotted sub-cells
  # for the structured list's fields (label, done).
  local INPUT_REF; INPUT_REF=$(pw_snap todo | grep -E 'textbox' | head -1 | grep -oE 'ref=e[0-9]+' | cut -d= -f2)
  local ADD_REF;   ADD_REF=$(pw_snap todo | grep 'button "Add"' | head -1 | grep -oE 'ref=e[0-9]+' | cut -d= -f2)

  $PWCLI -s=todo fill "$INPUT_REF" "walk the dog" > /dev/null
  $PWCLI -s=todo click "$ADD_REF" > /dev/null
  expect "first todo's label sub-cell" "$(pw_eval todo "window.sigil.cells['r1.label']")" "walk the dog"
  expect "first todo's done sub-cell"  "$(pw_eval todo "window.sigil.cells['r1.done']")" "false"
  expect "draft cleared after add"     "$(pw_eval todo "window.sigil.cells.c2")"        ""

  # Toggle the first todo's done flag — fires from the .done sub-cell.
  pw_eval todo "(function(){var f = window.sigil.cells.c1[0]+'.done'; window.sigil.apply({kind:'toggle',cell:f}); return 1;})()" > /dev/null
  expect "toggled .done flips to true" "$(pw_eval todo "window.sigil.cells['r1.done']")" "true"

  # The `if item.done` subtree mounts when toggled — ✓ should appear in
  # the row's DOM.
  local rows; rows=$(pw_eval todo "Array.from(document.querySelectorAll('[data-sigil-for-item]:not(template)')).map(function(e){return e.textContent;}).join('|')")
  expect_contains "toggle reveals ✓ in DOM" "$rows" "✓"

  # Add a second; verify ordering preserved.
  $PWCLI -s=todo fill "$INPUT_REF" "buy milk" > /dev/null
  $PWCLI -s=todo click "$ADD_REF" > /dev/null
  expect "list order preserved" "$(pw_eval todo "JSON.stringify(window.sigil.cells.c1)")" '["r1","r2"]'
  expect "second todo's label" "$(pw_eval todo "window.sigil.cells['r2.label']")" "buy milk"
  expect "second todo's done"  "$(pw_eval todo "window.sigil.cells['r2.done']")"  "false"

  # Remove first — sub-cells must be swept too, not just the parent.
  pw_eval todo "(function(){var t=window.sigil.cells.c1[0]; window.sigil.apply({kind:'remove_item',cell:'c1',args:{target:t}}); return 1;})()" > /dev/null
  expect "first row removed from list" "$(pw_eval todo "JSON.stringify(window.sigil.cells.c1)")" '["r2"]'
  expect "removed row's label gone" "$(pw_eval todo "typeof window.sigil.cells['r1.label']")" "undefined"
  expect "removed row's done gone"  "$(pw_eval todo "typeof window.sigil.cells['r1.done']")"  "undefined"

  session_done todo "$PID"
}

test_sigil_explore() {
  echo "[sigil explore]"
  # `sigil explore` is a dir-mode command — point it at examples/sigil/
  # and exercise per-example serving, source serving, the reactive
  # multi-iframe binding, SSE hot reload, and the themed error page.
  ./bin/sigil explore examples/sigil -a :9112 > "$TMP/explore.log" 2>&1 &
  local PID=$!
  sleep 1
  pw_open exp http://localhost:9112/

  # Two iframes: source pane + render pane.
  expect "two iframes (source + render)" \
    "$(pw_eval exp "document.querySelectorAll('iframe').length")" \
    "2"

  # Initial selection = first example alphabetically (benchmark).
  expect "source iframe initial src" \
    "$(pw_eval exp "document.querySelectorAll('iframe')[0].getAttribute('src')")" \
    "/source/benchmark"
  expect "render iframe initial src" \
    "$(pw_eval exp "document.querySelectorAll('iframe')[1].getAttribute('src')")" \
    "/example/benchmark"

  # Multi-statement handler updates BOTH iframe URLs in one click.
  local COUNTER_REF; COUNTER_REF=$(pw_snap exp | grep 'button "counter"' | head -1 | grep -oE 'ref=e[0-9]+' | cut -d= -f2)
  $PWCLI -s=exp click "$COUNTER_REF" > /dev/null
  expect "click retargets source iframe" \
    "$(pw_eval exp "document.querySelectorAll('iframe')[0].getAttribute('src')")" \
    "/source/counter"
  expect "click retargets render iframe" \
    "$(pw_eval exp "document.querySelectorAll('iframe')[1].getAttribute('src')")" \
    "/example/counter"

  # Render iframe actually loads (contentDocument.title resolves).
  expect "iframe content loads (counter)" \
    "$(pw_eval exp "document.querySelectorAll('iframe')[1].contentDocument && document.querySelectorAll('iframe')[1].contentDocument.title")" \
    "Sigil — Counter"

  # SSE hot reload: stash a sentinel on window, touch a .sigil file,
  # the page must reload (sentinel is gone). This is the single most
  # important DX feature in the command — verify it directly.
  pw_eval exp "(function(){window.__hotProbe = 'before'; return 1;})()" > /dev/null
  touch examples/sigil/counter.sigil
  sleep 1
  expect "SSE reload fires on .sigil change" \
    "$(pw_eval exp "typeof window.__hotProbe")" \
    "undefined"

  session_done exp $PID

  # Error page in a separate explorer instance with a broken example.
  echo "[sigil explore: error page]"
  local errDir; errDir=$(mktemp -d -t sig-err.XXXXXX)
  cat > "$errDir/broken.sigil" <<'EOF'
view Broken =
  not-a-real-kind "oops"
EOF
  ./bin/sigil explore "$errDir" -a :9113 > "$TMP/explore-err.log" 2>&1 &
  local EPID=$!
  sleep 1
  local body; body=$(curl -s http://localhost:9113/example/broken)
  if [[ "$body" == *"Compile error"* && "$body" == *"unknown component"* ]]; then
    echo "  ✓ broken example renders themed error page (not plain 500)"
    PASS=$((PASS + 1))
  else
    echo "  ✗ broken example error page malformed"
    FAIL=$((FAIL + 1))
    FAILURES+=("explore: error page malformed")
  fi
  kill $EPID 2>/dev/null
  rm -rf "$errDir"
}

test_sigil_brand_theme() {
  echo "[sigil/brand-theme.sigil]"
  local PID; PID=$(start_sigil examples/sigil/brand-theme.sigil 9111)
  pw_open brand http://localhost:9111/

  # Source-declared primary tone overrides the default light primary.
  expect "light primary = source #ff6b35" \
    "$(pw_eval brand "getComputedStyle(document.querySelector('button[data-sigil-tone=\"primary\"]')).backgroundColor")" \
    "rgb(255, 107, 53)"

  # Unspecified tones still fall through to defaults.
  expect "light danger = default #dc2626" \
    "$(pw_eval brand "getComputedStyle(document.querySelector('button[data-sigil-tone=\"danger\"]')).backgroundColor")" \
    "rgb(220, 38, 38)"

  pw_eval brand "(function(){window.sigil.theme.setColorMode('dark'); return 1;})()" > /dev/null
  expect "dark primary = source #ff8c5a" \
    "$(pw_eval brand "getComputedStyle(document.querySelector('button[data-sigil-tone=\"primary\"]')).backgroundColor")" \
    "rgb(255, 140, 90)"

  session_done brand "$PID"
}

# Verifies the compile-time contrast validator rejects bad themes. We
# don't need a server — just `sigil check` against a temp file.
test_sigil_contrast_rejection() {
  echo "[contrast-validator]"
  local TMP; TMP=$(mktemp -t bad-theme.XXXXXX.sigil)
  cat > "$TMP" <<'EOF'
theme bad extends light =
  primary = "#ff6b35" on "#ffffff"
view X =
  text "hi"
EOF
  local out; out=$(./bin/sigil check "$TMP" 2>&1)
  if [[ "$out" == *"WCAG AA"* ]]; then
    echo "  ✓ rejected sub-AA theme at compile time"
    PASS=$((PASS + 1))
  else
    echo "  ✗ failed to reject sub-AA theme; output: $out"
    FAIL=$((FAIL + 1))
    FAILURES+=("contrast-validator: did not reject")
  fi
  rm -f "$TMP"
}

test_sigil_tones() {
  echo "[sigil/tones.sigil]"
  local PID; PID=$(start_sigil examples/sigil/tones.sigil 9110)
  pw_open tones http://localhost:9110/

  # Light theme: tone=primary maps to charcoal (the default theme's primary
  # color #18181b → rgb(24,24,27)). The default-tone button uses the same
  # token so it should match.
  local primary_light; primary_light=$(pw_eval tones "getComputedStyle(document.querySelector('button[data-sigil-tone=\"primary\"]')).backgroundColor")
  expect "light primary tone realizes charcoal" "$primary_light" "rgb(24, 24, 27)"

  local danger_light; danger_light=$(pw_eval tones "getComputedStyle(document.querySelector('button[data-sigil-tone=\"danger\"]')).backgroundColor")
  expect "light danger tone realizes red" "$danger_light" "rgb(220, 38, 38)"

  # Flip to dark. Same source, different realization.
  pw_eval tones "(function(){window.sigil.theme.setColorMode('dark'); return 1;})()" > /dev/null
  local primary_dark; primary_dark=$(pw_eval tones "getComputedStyle(document.querySelector('button[data-sigil-tone=\"primary\"]')).backgroundColor")
  expect "dark primary tone realizes light" "$primary_dark" "rgb(250, 250, 250)"

  local body_dark; body_dark=$(pw_eval tones "getComputedStyle(document.body).backgroundColor")
  expect "dark page bg is near-black" "$body_dark" "rgb(10, 10, 10)"

  # Layer high-contrast modifier on top of dark.
  pw_eval tones "(function(){window.sigil.theme.setContrast('high'); return 1;})()" > /dev/null
  local primary_hc; primary_hc=$(pw_eval tones "getComputedStyle(document.querySelector('button[data-sigil-tone=\"primary\"]')).backgroundColor")
  expect "dark+high-contrast primary is pure white" "$primary_hc" "rgb(255, 255, 255)"
  local body_hc; body_hc=$(pw_eval tones "getComputedStyle(document.body).backgroundColor")
  expect "dark+high-contrast body is pure black" "$body_hc" "rgb(0, 0, 0)"

  # Reset and verify the runtime exposes system-signal cells.
  pw_eval tones "(function(){window.sigil.theme.setColorMode('light'); window.sigil.theme.setContrast('normal'); return 1;})()" > /dev/null
  local has_system; has_system=$(pw_eval tones "typeof window.sigil.cells['system.color-mode']")
  expect "system.color-mode cell is exposed" "$has_system" "string"

  session_done tones "$PID"
}

test_sigil_benchmark() {
  echo "[sigil/benchmark.sigil]"
  local PID; PID=$(start_sigil examples/sigil/benchmark.sigil 9109)
  pw_open bench http://localhost:9109/

  # create_random
  pw_eval bench "window.sigil.apply({kind:'create_batch_random',cell:'c1',args:{count:50,replace:true}})" > /dev/null
  local n; n=$(pw_eval bench "window.sigil.cells.c1.length")
  expect "create_random builds N rows" "$n" "50"

  # update_every
  local first; first=$(pw_eval bench "window.sigil.cells[window.sigil.cells.c1[0]]")
  pw_eval bench "window.sigil.apply({kind:'update_every',cell:'c1',args:{stride:10,suffix:' !!!'}})" > /dev/null
  local first_after; first_after=$(pw_eval bench "window.sigil.cells[window.sigil.cells.c1[0]]")
  expect "update_every stride 10 hits row 0" "$first_after" "$first !!!"
  local row1_after; row1_after=$(pw_eval bench "window.sigil.cells[window.sigil.cells.c1[1]]")
  local row1_before; row1_before=$(pw_eval bench "window.sigil.cells[window.sigil.cells.c1[1]].replace(/ !!!\$/,'')")
  expect "update_every skips row 1" "$row1_after" "$row1_before"

  # swap
  local before_swap; before_swap=$(pw_eval bench "window.sigil.cells.c1[1] + '|' + window.sigil.cells.c1[40]")
  pw_eval bench "window.sigil.apply({kind:'swap_items',cell:'c1',args:{i:1,j:40}})" > /dev/null
  local after_swap; after_swap=$(pw_eval bench "window.sigil.cells.c1[40] + '|' + window.sigil.cells.c1[1]")
  expect "swap_items exchanges positions" "$after_swap" "$before_swap"

  # select sets variant on the target row only
  local target; target=$(pw_eval bench "window.sigil.cells.c1[5]")
  pw_eval bench "window.sigil.apply({kind:'select_in_list',cell:'c1',args:{target:'$target'}})" > /dev/null
  local sel; sel=$(pw_eval bench "document.querySelector('[data-sigil-for-item=\"$target\"]').getAttribute('data-sigil-tone-runtime')")
  expect "select_in_list sets tone-runtime=selected" "$sel" "selected"

  # selecting a different row clears the previous
  local target2; target2=$(pw_eval bench "window.sigil.cells.c1[10]")
  pw_eval bench "window.sigil.apply({kind:'select_in_list',cell:'c1',args:{target:'$target2'}})" > /dev/null
  local prev_after; prev_after=$(pw_eval bench "document.querySelector('[data-sigil-for-item=\"$target\"]').getAttribute('data-sigil-tone-runtime') || ''")
  expect "previous selection cleared" "$prev_after" ""

  # clear empties the list and the DOM
  pw_eval bench "window.sigil.apply({kind:'clear_list',cell:'c1'})" > /dev/null
  expect "clear empties list" "$(pw_eval bench 'window.sigil.cells.c1.length')" "0"
  expect "clear empties DOM rows" \
    "$(pw_eval bench "document.querySelectorAll('[data-sigil-for=c1] > div').length")" "0"

  session_done bench "$PID"
}

test_sigil_components() {
  echo "[sigil/components.sigil]"
  local PID; PID=$(start_sigil examples/sigil/components.sigil 9108)
  pw_open scomp http://localhost:9108/
  # Two state cells declared in the view: c1 (apples), c2 (oranges).
  # Each named-counter invocation inlines its own copy of the body, so
  # the bindings + handlers in each row point at the row's own cell.
  expect "initial apples" "$(read_cell scomp c1)" "5"
  expect "initial oranges" "$(read_cell scomp c2)" "3"

  # Mutate only the apples row. Multiple identical "+" labels would be
  # ambiguous via click — fire the action directly to prove each
  # invocation has its own substitution context.
  pw_eval scomp "window.sigil.apply({kind:'add',cell:'c1',args:{delta:1}})" > /dev/null
  expect "apples after +1" "$(read_cell scomp c1)" "6"
  expect "oranges unchanged" "$(read_cell scomp c2)" "3"

  session_done scomp "$PID"
}

test_sigil_form_echo() {
  echo "[sigil/form-echo.sigil]"
  local PID; PID=$(start_sigil examples/sigil/form-echo.sigil 9107)
  pw_open sform http://localhost:9107/
  expect "initial bound text" "$(read_cell sform c1)" "hello, "

  local INPUT
  INPUT=$(textbox_ref sform)
  if [ -z "$INPUT" ]; then
    echo "  ✗ textbox ref not found"
    FAIL=$((FAIL + 1)); FAILURES+=("sigil/form-echo: textbox ref")
  else
    $PWCLI -s=sform fill "$INPUT" "alice" > /dev/null
    expect "after typing alice" "$(read_cell sform c1)" "hello, alice"
  fi

  session_done sform "$PID"
}

test_sigil_counters() {
  echo "[sigil/counters.sigil]"
  local PID; PID=$(start_sigil examples/sigil/counters.sigil 9106)
  pw_open counters http://localhost:9106/
  # Three child cells c1 c2 c3, parent c4 = [c1, c2, c3]
  expect "row 1 initial" "$(read_cell counters c1)" "0"
  expect "row 2 initial" "$(read_cell counters c2)" "0"
  expect "row 3 initial" "$(read_cell counters c3)" "0"

  # Per-row mutation via direct apply (multiple identical button labels).
  pw_eval counters "window.sigil.apply({kind:'add',cell:'c2',args:{delta:1}})" > /dev/null
  pw_eval counters "window.sigil.apply({kind:'add',cell:'c2',args:{delta:1}})" > /dev/null
  expect "row 2 after +2" "$(read_cell counters c2)" "2"
  expect "row 1 unchanged" "$(read_cell counters c1)" "0"
  expect "row 3 unchanged" "$(read_cell counters c3)" "0"

  # `+ add counter` button (unique label) — uses .append() in source.
  local ADD; ADD=$(button_ref counters '+ add counter')
  $PWCLI -s=counters click "$ADD" > /dev/null
  expect "appended r1 = 0" "$(read_cell counters r1)" "0"

  # Mutate the freshly-appended row to prove its actions are live.
  pw_eval counters "window.sigil.apply({kind:'add',cell:'r1',args:{delta:9}})" > /dev/null
  expect "r1 after +9" "$(read_cell counters r1)" "9"

  # Remove the FIRST row. Multiple "x" buttons exist (one per row), so
  # click-by-name is ambiguous — instead fire the action that the row's
  # button is wired to. The button is the source of the action and uses
  # the exact same `items.remove(item)` we want to exercise.
  pw_eval counters "window.sigil.apply({kind:'remove_item',cell:'c4',args:{target:'c1'}})" > /dev/null
  local cells; cells=$(read_cells_json counters)
  expect_contains "list reordered after remove" "$cells" '"c4":["c2","c3","r1"]'

  session_done counters "$PID"
}

# ---------------- tests: Go examples ----------------

test_go_hello() {
  echo "[go/examples/hello]"
  local PID; PID=$(start_go examples/hello 9201)
  pw_open ghello http://localhost:9201/
  local snap; snap=$(pw_snap ghello)
  expect_contains "renders title" "$snap" 'Sigil'
  expect_contains "renders text"  "$snap" 'Hello, world'
  session_done ghello "$PID"
}

test_go_counter() {
  echo "[go/examples/counter]"
  local PID; PID=$(start_go examples/counter 9202)
  pw_open gcount http://localhost:9202/
  expect "initial cell c1" "$(read_cell gcount c1)" "0"

  local PLUS MINUS
  PLUS=$(button_ref gcount '+')
  MINUS=$(button_ref gcount '−')

  $PWCLI -s=gcount click "$PLUS" > /dev/null
  $PWCLI -s=gcount click "$PLUS" > /dev/null
  expect "after ++" "$(read_cell gcount c1)" "2"

  $PWCLI -s=gcount click "$MINUS" > /dev/null
  expect "after -"  "$(read_cell gcount c1)" "1"

  session_done gcount "$PID"
}

test_go_disclosure() {
  echo "[go/examples/disclosure]"
  local PID; PID=$(start_go examples/disclosure 9203)
  pw_open gdisc http://localhost:9203/
  # cell c1=expanded, c2=count (matches Go-side State allocation order)
  local snap; snap=$(pw_snap gdisc)
  if [[ "$snap" == *"Hidden by default"* ]]; then
    echo "  ✗ hidden leaked initially"
    FAIL=$((FAIL + 1)); FAILURES+=("go/disclosure: hidden leaked")
  else
    echo "  ✓ hidden inert initially"
    PASS=$((PASS + 1))
  fi

  local TOGGLE PLUS
  TOGGLE=$(button_ref gdisc 'Toggle')
  PLUS=$(button_ref gdisc '+')

  $PWCLI -s=gdisc click "$TOGGLE" > /dev/null
  snap=$(pw_snap gdisc)
  expect_contains "after Toggle: revealed" "$snap" 'Hidden by default'

  $PWCLI -s=gdisc click "$PLUS" > /dev/null
  expect "+ updates count" "$(read_cell gdisc c2)" "1"

  session_done gdisc "$PID"
}

test_go_form_echo() {
  echo "[go/examples/form-echo]"
  local PID; PID=$(start_go examples/form-echo 9204)
  pw_open gform http://localhost:9204/
  expect "initial echo empty" "$(read_cell gform c1)" ""

  local INPUT
  INPUT=$(textbox_ref gform)
  if [ -z "$INPUT" ]; then
    echo "  ✗ textbox ref not found"
    FAIL=$((FAIL + 1)); FAILURES+=("go/form-echo: textbox ref")
  else
    $PWCLI -s=gform fill "$INPUT" "alice" > /dev/null
    expect "after typing alice" "$(read_cell gform c1)" "alice"
  fi

  session_done gform "$PID"
}

test_go_list() {
  echo "[go/examples/list]"
  local PID; PID=$(start_go examples/list 9205)
  pw_open glist http://localhost:9205/
  # Go's ListState allocates parent first then children, so cells are:
  #   c1 = parent list  ["c2","c3","c4"]
  #   c2, c3, c4 = three child cells (each 0)
  # (Sigil-language Lower does the opposite order — children first then
  # parent. Either works; the wire format is identical.)
  expect "row 1 initial" "$(read_cell glist c2)" "0"
  expect "row 2 initial" "$(read_cell glist c3)" "0"
  expect "row 3 initial" "$(read_cell glist c4)" "0"

  # Append a fresh row. Runtime mints client-side r1.
  pw_eval glist "window.sigil.apply({kind:'append_item',cell:'c1',args:{value:0}})" > /dev/null
  expect "after append: r1 = 0" "$(read_cell glist r1)" "0"

  # Mutate row 2 (cell c3): only that row changes.
  pw_eval glist "window.sigil.apply({kind:'add',cell:'c3',args:{delta:5}})" > /dev/null
  expect "row 2 after +5"     "$(read_cell glist c3)" "5"
  expect "row 1 unchanged"    "$(read_cell glist c2)" "0"
  expect "row 3 unchanged"    "$(read_cell glist c4)" "0"

  # Remove row 1 (c2) from parent list c1.
  pw_eval glist "window.sigil.apply({kind:'remove_item',cell:'c1',args:{target:'c2'}})" > /dev/null
  local cells; cells=$(read_cells_json glist)
  expect_contains "c1 reordered after remove" "$cells" '"c1":["c3","c4","r1"]'

  session_done glist "$PID"
}

# ---------------- main ----------------

echo "=== Sigil example test suite ==="
echo

# Sigil-language examples
test_sigil_hello
test_sigil_layout
test_sigil_counter
test_sigil_echo
test_sigil_disclosure
test_sigil_counters
test_sigil_components
test_sigil_tones
test_sigil_brand_theme
test_sigil_contrast_rejection
test_sigil_benchmark
test_sigil_form_echo
test_sigil_todo
test_sigil_explore
echo
# Go-library examples
test_go_hello
test_go_counter
test_go_disclosure
test_go_form_echo
test_go_list

echo
echo "================================"
echo "PASS: $PASS    FAIL: $FAIL"
if [ $FAIL -gt 0 ]; then
  echo "Failures:"
  for f in "${FAILURES[@]}"; do
    echo "  - $f"
  done
  exit 1
fi
