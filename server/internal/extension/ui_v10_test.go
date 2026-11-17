package extension

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func v10Example(t *testing.T, name string) *View {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "extension", "examples", "View.v10-"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseView(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

var v10Examples = []string{"work-tree", "plan-layered"}

// graphOf is the first graph of v.
func graphOf(t *testing.T, v *View) *UIElem {
	t.Helper()
	for _, e := range items(v) {
		if e.Type == wire.ComponentGraph {
			return &e
		}
	}
	t.Fatal("no graph left")
	return nil
}

// An app of UI version 10 gets every example as the extension drew it. An app of 1 to 9 gets rows instead: every
// visible node in reading order, indented by its depth, carrying its subtitle, badge, icon, color, progress and
// action. A folded node's row says how many it hides and they have no row; the key is the graph's and the node's only
// when free.
func TestFitV10Examples(t *testing.T) {
	page, _ := Slot("nav_page")
	for _, name := range v10Examples {
		v := v10Example(t, name)
		want, _ := json.Marshal(v)
		if dropped, err := Fit(v, page, 10); err != nil || len(dropped) > 0 {
			t.Fatalf("%s for 10: dropped %v, %v", name, dropped, err)
		}
		if got, _ := json.Marshal(v); string(got) != string(want) {
			t.Errorf("%s: an app of 10 gets it changed\nwant %s\n got %s", name, want, got)
		}
	}
}

// What the release checklist says for an app of 9: every visible node in reading order, its indent, its text, its
// percentage, and the folded node's count with its descendants left out.
func TestFitV10WorkTreeRows(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v10Example(t, "work-tree")
	drawn := graphOf(t, v)
	order, depth := newGraphWalk(drawn).readingOrder()
	if _, err := Fit(v, page, 9); err != nil {
		t.Fatal(err)
	}
	all := items(v)
	if len(all) != 1+len(order) {
		t.Fatalf("%d items for the header and %d visible nodes: %+v", len(all), len(order), all)
	}
	rows := all[1:]
	for k, row := range rows {
		id := order[k]
		if row.Type != wire.ComponentRow {
			t.Fatalf("row %d is a %s", k, row.Type)
		}
		indent := strings.Repeat("│ ", max(depth[id]-1, 0))
		if depth[id] > 0 {
			indent += "└ "
		}
		node := nodeOf(t, drawn, id)
		if row.Title != indent+node.Title {
			t.Errorf("row %d: title %q, want %q", k, row.Title, indent+node.Title)
		}
		if row.Subtitle != node.Subtitle || row.Icon != node.Icon || row.IconColor != node.Color {
			t.Errorf("row %d (%s): %+v, want the node's subtitle, icon and color", k, id, row)
		}
		switch {
		case node.Progress != nil && row.Value != gaugePercent(*node.Progress):
			t.Errorf("row %d (%s): value %q for progress %v", k, id, row.Value, *node.Progress)
		case node.Progress == nil && row.Value != "" && id != "docs":
			t.Errorf("row %d (%s): value %q without progress", k, id, row.Value)
		}
		if row.Key != "work/"+id {
			t.Errorf("row %d (%s): key %q", k, id, row.Key)
		}
		if (row.Action == nil) != (node.Action == nil) {
			t.Errorf("row %d (%s): action %v, want the node's", k, id, row.Action)
		}
	}
	if rows[9].Title != "└ Docs" || rows[9].Value != "+2" {
		t.Errorf("the folded node: %q / %q", rows[9].Title, rows[9].Value)
	}
	for _, hidden := range []string{"English", "Japanese"} {
		if slices.ContainsFunc(rows, func(r UIElem) bool { return strings.HasSuffix(r.Title, hidden) }) {
			t.Errorf("%s has a row under the folded Docs", hidden)
		}
	}
}

// A folded node keeps its percentage and gains the count after it; every node has its percentage rounded, and a
// graph without a key gives no row keys.
func TestGraphRowValue(t *testing.T) {
	page, _ := Slot("nav_page")
	v := &View{Sections: []Section{{Items: []UIElem{{Type: wire.ComponentGraph, Nodes: []wire.GraphNode{
		{ID: "r", Title: "r", Progress: num(0.755)},
		{ID: "a", Title: "a", Progress: num(0.45)},
		{ID: "b", Title: "b"},
	}, Edges: []wire.GraphEdge{{From: "r", To: "a"}, {From: "a", To: "b"}}, Folded: []string{"a"}}}}}}
	if _, err := Fit(v, page, 9); err != nil {
		t.Fatal(err)
	}
	rows := items(v)
	if len(rows) != 2 || rows[0].Value != "76%" || rows[1].Value != "45% · +1" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Key != "" || rows[1].Key != "" {
		t.Fatalf("keys without the graph's: %+v", rows)
	}
}

// Below 6 the rows lose their color and below 3 their icon, as any row.
func TestFitV10RowsForOldApps(t *testing.T) {
	page, _ := Slot("nav_page")
	for version, keepIcon := range map[int]bool{5: true, 2: false} {
		v := &View{Sections: []Section{{Items: []UIElem{{Type: wire.ComponentGraph, Nodes: []wire.GraphNode{
			{ID: "a", Title: "a", Icon: "list", Color: "green"},
			{ID: "b", Title: "b", Icon: "check"},
		}, Edges: []wire.GraphEdge{{From: "a", To: "b"}}}}}}}
		if _, err := Fit(v, page, version); err != nil {
			t.Fatalf("for %d: %v", version, err)
		}
		rows := items(v)
		if len(rows) != 2 {
			t.Fatalf("for %d: %+v", version, rows)
		}
		for _, row := range rows {
			if row.IconColor != "" {
				t.Fatalf("for %d: row %q keeps icon_color %q", version, row.Title, row.IconColor)
			}
			if (row.Icon != "") != keepIcon {
				t.Fatalf("for %d: row %q has icon %q", version, row.Title, row.Icon)
			}
		}
	}
}

// A native stream of 10 still gets rows: a stream's components never include a graph.
func TestFitNativeV10(t *testing.T) {
	g := UIElem{Type: wire.ComponentGraph, Key: "g",
		Nodes: []wire.GraphNode{{ID: "a", Title: "one"}, {ID: "a.b", Title: "two"}},
		Edges: []wire.GraphEdge{{From: "a", To: "a.b"}}}
	raw, err := json.Marshal(&View{Sections: []Section{{Key: "s", Items: []UIElem{g}}}})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := Slot("nav_page")
	fitted, err := FitNative(raw, page, 10, i18n.En)
	if err != nil {
		t.Fatal(err)
	}
	var got View
	if err := json.Unmarshal(fitted, &got); err != nil {
		t.Fatal(err)
	}
	rows := got.Sections[0].Items
	if len(rows) != 2 || rows[0].Type != wire.ComponentRow || rows[1].Title != "└ two" || rows[1].Key != "g/a.b" {
		t.Fatalf("native 10: %s", fitted)
	}
}

// nodeOf is id's node of g.
func nodeOf(t *testing.T, g *UIElem, id string) *wire.GraphNode {
	t.Helper()
	for k := range g.Nodes {
		if g.Nodes[k].ID == id {
			return &g.Nodes[k]
		}
	}
	t.Fatalf("no node %q", id)
	return nil
}

// The reading order, its depths and the fold counts are the shared cases' (spec/fixtures/graph-layout).
func TestGraphReadingOrderFollowsTheSharedCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "graph-layout", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name  string
			Graph struct {
				Nodes  []struct{ ID, Group string }
				Edges  []struct{ From, To string }
				Layout string
				Folded []string
			}
			Expected struct {
				Order []string
				Depth map[string]int
				Folds map[string]int
			}
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range doc.Cases {
		g := &wire.Component{Layout: c.Graph.Layout, Folded: c.Graph.Folded}
		for _, n := range c.Graph.Nodes {
			g.Nodes = append(g.Nodes, wire.GraphNode{ID: n.ID, Title: n.ID, Group: n.Group})
		}
		for _, e := range c.Graph.Edges {
			g.Edges = append(g.Edges, wire.GraphEdge{From: e.From, To: e.To})
		}
		w := newGraphWalk(g)
		order, depth := w.readingOrder()
		folds := map[string]int{}
		for _, id := range order {
			if len(w.children[id]) > 0 {
				folds[id] = 0
				if w.folded[id] {
					folds[id] = w.hidden(id)
				}
			}
		}
		if !slices.Equal(order, c.Expected.Order) {
			t.Errorf("%s: order %v, want %v", c.Name, order, c.Expected.Order)
		}
		if !maps.Equal(depth, c.Expected.Depth) {
			t.Errorf("%s: depth %v, want %v", c.Name, depth, c.Expected.Depth)
		}
		if !maps.Equal(folds, c.Expected.Folds) {
			t.Errorf("%s: folds %v, want %v", c.Name, folds, c.Expected.Folds)
		}
	}
}
