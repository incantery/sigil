package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/theme"
)

var describeJSON bool

// renderOpSig renders one query/command signature in source-like
// form: `Name(arg1: Type1, arg2: Type2) -> ReturnType`. Used by
// `sigil describe` to print the queries / commands sections.
func renderOpSig(name string, inputs []ir.TypeFieldSpec, ret ir.TypeRef) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteString("(")
	for i, in := range inputs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(in.Name)
		b.WriteString(": ")
		b.WriteString(renderTypeRef(in.Type))
	}
	b.WriteString(") -> ")
	b.WriteString(renderTypeRef(ret))
	return b.String()
}

// renderTypeRef renders an ir.TypeRef as it would appear in source:
// `List<Pokemon>?`, `Int`, etc. Used by `sigil describe` and by the
// formatter (which re-imports from here would create a cycle, so
// they share the algorithm — keep them in sync until we lift it
// into a shared package).
func renderTypeRef(t ir.TypeRef) string {
	out := t.Name
	if len(t.GenericArgs) > 0 {
		out += "<"
		for i, a := range t.GenericArgs {
			if i > 0 {
				out += ", "
			}
			out += renderTypeRef(a)
		}
		out += ">"
	}
	if t.Optional {
		out += "?"
	}
	return out
}

var describeCmd = &cobra.Command{
	Use:   "describe <file.mako>",
	Short: "Print a structured description of what a Sigil file renders",
	Long: `Lowers a .mako file to IR and prints a tree-shaped description of
the rendered output — without launching a browser. Useful in editor/AI
loops to verify "did my change produce the structure I intended?" without
a screenshot round-trip. Pass --json to emit the full IR document instead.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		doc, err := compileFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return ErrSilent
		}
		if describeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(doc)
		}
		describeTree(os.Stdout, doc)
		return nil
	},
}

// describeTree prints a box-drawing tree of the IR rooted at doc.Root.
// Each node line is "<kind> <interesting-props-inline>". The view name
// (from the .mako source) prefixes the tree as a header; the cell table
// sits between the header and the tree.
func describeTree(w io.Writer, doc ir.Document) {
	header := "view " + doc.Name
	if doc.Name == "" {
		header = "view <unnamed>"
	}
	fmt.Fprintln(w, header)
	if len(doc.Themes) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "themes:")
		for _, t := range doc.Themes {
			th, ok := t.(*theme.Theme)
			if !ok {
				continue
			}
			base := th.ExtendsName
			if base == "" {
				base = "(none)"
			}
			// List only the tones the source actually overrode, not the
			// full inherited set — keeps the output focused on what the
			// author declared.
			overridden := []string{}
			defaults := theme.Light.Tones
			if base == "dark" {
				defaults = theme.Dark.Tones
			}
			for name, pair := range th.Tones {
				if d, ok := defaults[name]; !ok || d != pair {
					overridden = append(overridden, name)
				}
			}
			sort.Strings(overridden)
			fmt.Fprintf(w, "  %s extends %s (overrides: %s)\n",
				th.Name, base, strings.Join(overridden, ", "))
		}
	}
	if len(doc.Components) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "components:")
		for _, c := range doc.Components {
			fmt.Fprintf(w, "  %s(%s)\n", c.Name, strings.Join(c.Params, ", "))
		}
	}
	if len(doc.Types) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "types:")
		for _, t := range doc.Types {
			fmt.Fprintf(w, "  %s\n", t.Name)
			for _, f := range t.Fields {
				fmt.Fprintf(w, "    %s : %s\n", f.Name, renderTypeRef(f.Type))
			}
			for _, v := range t.Variants {
				fmt.Fprintf(w, "    | %s\n", v)
			}
		}
	}
	if len(doc.Queries) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "queries:")
		for _, q := range doc.Queries {
			fmt.Fprintf(w, "  %s\n", renderOpSig(q.Name, q.Inputs, q.Return))
		}
	}
	if len(doc.Commands) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "commands:")
		for _, c := range doc.Commands {
			fmt.Fprintf(w, "  %s\n", renderOpSig(c.Name, c.Inputs, c.Return))
		}
	}
	if len(doc.Cells) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "state:")
		writeCellsTable(w, doc)
	}
	fmt.Fprintln(w)
	walkNode(w, doc.Root, "", true)
}

// writeCellsTable prints each cell as "  cN (name): Type = value". Names
// come from doc.CellNames (only set for cells the .mako source named —
// list cell *children* are typically anonymous, so they show as "  cN:
// Type = value" without parens). Sorted by id for stable output.
func writeCellsTable(w io.Writer, doc ir.Document) {
	ids := make([]string, 0, len(doc.Cells))
	for id := range doc.Cells {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		value := doc.Cells[id]
		name := doc.CellNames[id]
		nameSuffix := ""
		if name != "" {
			nameSuffix = " (" + name + ")"
		}
		fmt.Fprintf(w, "  %s%s: %s = %s\n",
			id, nameSuffix, cellTypeName(value), cellValueRepr(value))
	}
}

// cellTypeName returns a friendly type label for a cell value.
func cellTypeName(v any) string {
	switch v.(type) {
	case bool:
		return "Bool"
	case int, int32, int64:
		return "Int"
	case float32, float64:
		return "Float"
	case string:
		return "String"
	case []string:
		return "List"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// cellValueRepr returns a compact display of a cell's value.
func cellValueRepr(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case []string:
		return "[" + strings.Join(x, ", ") + "]"
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// walkNode prints n preceded by its connector, then recurses.
// prefix carries the trailing whitespace/bars for deeper levels.
func walkNode(w io.Writer, n ir.Node, prefix string, isLast bool) {
	connector := "├─ "
	nextPrefix := prefix + "│  "
	if isLast {
		connector = "└─ "
		nextPrefix = prefix + "   "
	}
	fmt.Fprintf(w, "%s%s%s\n", prefix, connector, summarize(n))
	for i, child := range n.Children {
		walkNode(w, child, nextPrefix, i == len(n.Children)-1)
	}
}

// summarize returns the one-line "kind <inline props>" string for a node.
// Only props worth eyeballing are inlined; the rest live in --json.
func summarize(n ir.Node) string {
	parts := []string{string(n.Kind)}

	switch n.Kind {
	case ir.KindText, ir.KindTitle:
		if t, ok := n.Props["text"].(string); ok && t != "" {
			parts = append(parts, strconv.Quote(t))
		}
	case ir.KindButton:
		if l, ok := n.Props["label"].(string); ok && l != "" {
			parts = append(parts, strconv.Quote(l))
		}
	case ir.KindStack:
		if axis, ok := n.Props["axis"].(string); ok && axis == "horizontal" {
			parts = append(parts, "horizontal")
		}
		if gap, ok := n.Props["gap"].(int); ok && gap > 0 {
			parts = append(parts, fmt.Sprintf("gap=%d", gap))
		}
	case ir.KindTextInput:
		if p, ok := n.Props["placeholder"].(string); ok && p != "" {
			parts = append(parts, fmt.Sprintf("placeholder=%q", p))
		}
	case ir.KindIf:
		if b, ok := n.Bindings["visible"]; ok {
			parts = append(parts, "when="+b.CellID)
		}
	case ir.KindFor:
		if c, ok := n.Props["cell"].(string); ok {
			parts = append(parts, "over="+c)
		}
	}

	// Text bindings show "→ cellId" so it's obvious a node is reactive.
	if b, ok := n.Bindings["text"]; ok {
		parts = append(parts, "→ "+b.CellID)
	}
	if b, ok := n.Bindings["value"]; ok {
		parts = append(parts, "↔ "+b.CellID)
	}

	// Handlers: list event=action.Kind in sorted order for stable output.
	if len(n.Handlers) > 0 {
		keys := make([]string, 0, len(n.Handlers))
		for k := range n.Handlers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("on:%s=%s", k, n.Handlers[k].Kind))
		}
	}

	return strings.Join(parts, " ")
}

func init() {
	describeCmd.Flags().BoolVar(&describeJSON, "json", false,
		"emit the full IR document as JSON instead of a text tree")
}
