view Benchmark =
  state rows = []
  card
    title "Sigil benchmark"
    stack horizontal gap=1
      button "Create 1,000"   on click { rows.create_random(1000) }
      button "Create 10,000"  on click { rows.create_random(10000) }
      button "Append 1,000"   on click { rows.append_random(1000) }
      button "Update 10th"    on click { rows.update_every(10, " !!!") }
      button "Clear"          on click { rows.clear() }
      button "Swap"           on click { rows.swap(1, 998) }
    for row in rows
      stack horizontal gap=1
        text row
        button "select" on click { rows.select(row) }
        button "x" on click { rows.remove(row) }

test "Create 1,000 fills the list" = scenario Benchmark
  click button "Create 1,000"
  wait 200

test "Clear empties the list after create" = scenario Benchmark
  click button "Create 1,000"
  wait 100
  click button "Clear"
  wait 50

test "Append adds to existing rows" = scenario Benchmark
  click button "Create 1,000"
  wait 100
  click button "Append 1,000"
  wait 100

test "Swap exchanges two rows" = scenario Benchmark
  click button "Create 1,000"
  wait 100
  click button "Swap"

test "Update 10th appends suffix to every Nth row" = scenario Benchmark
  click button "Create 1,000"
  wait 100
  click button "Update 10th"
