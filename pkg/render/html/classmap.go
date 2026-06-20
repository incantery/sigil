package html

import (
	"fmt"
	"strings"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/style"
)

// BuildClassMap walks the IR tree and resolves every node's CSS classes
// via the style resolver. Returns a map from node ID to space-separated
// class string. Also drives icon-usage tracking so the resolver knows
// which SVG symbols to include in the <defs> block.
//
// Sub-element keys use the composite convention: "nodeID/fill" for the
// bar's inner fill element.
func BuildClassMap(root ir.Node, r *resolver) map[string]string {
	m := make(map[string]string)
	buildClassMapWalk(root, r, m)
	return m
}

func buildClassMapWalk(n ir.Node, r *resolver, m map[string]string) {
	switch n.Kind {
	case ir.KindIcon:
		spec := style.SpecFor(n)
		classes := append([]string{"s-icon"}, r.classes(spec)...)
		m[n.ID] = strings.Join(classes, " ")
		r.useIcon(cmPropStr(n, "icon-set"), cmPropStr(n, "name"))

	case ir.KindPulse:
		spec := style.SpecFor(n)
		classes := append([]string{"s-pulse"}, r.classes(spec)...)
		m[n.ID] = strings.Join(classes, " ")

	case ir.KindBar:
		spec := style.SpecFor(n)
		outerClasses := append([]string{"s-bar-track"}, r.classes(spec)...)
		m[n.ID] = strings.Join(outerClasses, " ")

		fillColor := "accent"
		if c := spec.Color; c != "" {
			fillColor = string(c)
		}
		fillClasses := []string{"s-bar-fill"}
		fillClasses = append(fillClasses, r.simpleClass("barfill", fillColor,
			fmt.Sprintf("background:var(--color-%s-bg);", fillColor)))
		m[n.ID+"/fill"] = strings.Join(fillClasses, " ")

	case ir.KindButton:
		spec := style.SpecFor(n)
		if classes := r.classes(spec); len(classes) > 0 {
			m[n.ID] = strings.Join(classes, " ")
		}
		if iconName, ok := n.Props["icon"].(string); ok {
			iconSet, _ := n.Props["icon-set"].(string)
			r.useIcon(iconSet, iconName)
		}

	case ir.KindFor:
		spec := style.Spec{Direction: style.DirColumn, Gap: 1}
		if classes := r.classes(spec); len(classes) > 0 {
			m[n.ID] = strings.Join(classes, " ")
		}

	default:
		spec := style.SpecFor(n)
		if classes := r.classes(spec); len(classes) > 0 {
			m[n.ID] = strings.Join(classes, " ")
		}
	}

	for _, c := range n.Children {
		buildClassMapWalk(c, r, m)
	}
}

func cmPropStr(n ir.Node, k string) string {
	if v, ok := n.Props[k].(string); ok {
		return v
	}
	return ""
}
