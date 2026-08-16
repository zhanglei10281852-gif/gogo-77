// Package catchment holds the topology of a monitored catchment and propagates
// site signals through it.
//
// Sites are nodes and links are directed edges pointing downstream. The graph
// must be acyclic: water does not return to where it came from, and a cycle
// would make a roll-up either infinite or arbitrary. Build rejects cycles with
// the offending path named, and every traversal walks a fixed topological order
// derived from sorted identifiers, so two runs visit the nodes identically.
package catchment

import (
	"fmt"
	"sort"
	"strings"

	"FluWatershed/internal/model"
	"FluWatershed/internal/numeric"
)

// Graph is an immutable view of a validated catchment topology.
type Graph struct {
	sites      map[string]model.Site
	ids        []string
	downstream map[string][]string
	upstream   map[string][]string
	travel     map[string]float64
	order      []string
}

func edgeKey(from, to string) string { return from + "->" + to }

// Build constructs the graph, rejecting unknown endpoints, self links,
// duplicate links and cycles.
func Build(network model.Network) (*Graph, error) {
	graph := &Graph{
		sites:      make(map[string]model.Site, len(network.Sites)),
		downstream: make(map[string][]string),
		upstream:   make(map[string][]string),
		travel:     make(map[string]float64),
	}
	for _, site := range network.Sites {
		if _, clash := graph.sites[site.SiteID]; clash {
			return nil, fmt.Errorf("site %s is declared more than once", site.SiteID)
		}
		graph.sites[site.SiteID] = site
		graph.ids = append(graph.ids, site.SiteID)
	}
	sort.Strings(graph.ids)
	for _, link := range network.Links {
		if link.UpstreamSiteID == link.DownstreamSiteID {
			return nil, fmt.Errorf("site %s cannot link to itself", link.UpstreamSiteID)
		}
		if _, ok := graph.sites[link.UpstreamSiteID]; !ok {
			return nil, fmt.Errorf("link %s names an unknown upstream site", edgeKey(link.UpstreamSiteID, link.DownstreamSiteID))
		}
		if _, ok := graph.sites[link.DownstreamSiteID]; !ok {
			return nil, fmt.Errorf("link %s names an unknown downstream site", edgeKey(link.UpstreamSiteID, link.DownstreamSiteID))
		}
		key := edgeKey(link.UpstreamSiteID, link.DownstreamSiteID)
		if _, clash := graph.travel[key]; clash {
			return nil, fmt.Errorf("link %s is declared more than once", key)
		}
		graph.travel[key] = link.TravelHours
		graph.downstream[link.UpstreamSiteID] = append(graph.downstream[link.UpstreamSiteID], link.DownstreamSiteID)
		graph.upstream[link.DownstreamSiteID] = append(graph.upstream[link.DownstreamSiteID], link.UpstreamSiteID)
	}
	for _, id := range graph.ids {
		sort.Strings(graph.downstream[id])
		sort.Strings(graph.upstream[id])
	}
	order, err := graph.topologicalOrder()
	if err != nil {
		return nil, err
	}
	graph.order = order
	return graph, nil
}

// topologicalOrder returns upstream-before-downstream order. Ties are broken by
// identifier so the order is unique for a given topology. A remaining node with
// unsatisfied predecessors means a cycle, and the cycle is traced for the error.
func (g *Graph) topologicalOrder() ([]string, error) {
	remaining := make(map[string]int, len(g.ids))
	for _, id := range g.ids {
		remaining[id] = len(g.upstream[id])
	}
	ready := make([]string, 0, len(g.ids))
	for _, id := range g.ids {
		if remaining[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(g.ids))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		for _, next := range g.downstream[current] {
			remaining[next]--
			if remaining[next] == 0 {
				ready = append(ready, next)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(g.ids) {
		stuck := make([]string, 0, len(g.ids)-len(order))
		placed := make(map[string]bool, len(order))
		for _, id := range order {
			placed[id] = true
		}
		for _, id := range g.ids {
			if !placed[id] {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		if cycle := g.traceCycle(stuck); len(cycle) > 0 {
			return nil, fmt.Errorf("catchment topology is cyclic: %s", strings.Join(cycle, " -> "))
		}
		return nil, fmt.Errorf("catchment topology is cyclic among %s", strings.Join(stuck, ", "))
	}
	return order, nil
}

// traceCycle walks downstream from the first stuck node until it revisits a
// node, returning that loop.
func (g *Graph) traceCycle(stuck []string) []string {
	if len(stuck) == 0 {
		return nil
	}
	inCycle := make(map[string]bool, len(stuck))
	for _, id := range stuck {
		inCycle[id] = true
	}
	position := make(map[string]int, len(stuck))
	path := make([]string, 0, len(stuck)+1)
	current := stuck[0]
	for step := 0; step <= len(stuck); step++ {
		if index, seen := position[current]; seen {
			return append(path[index:], current)
		}
		position[current] = len(path)
		path = append(path, current)
		next := ""
		for _, candidate := range g.downstream[current] {
			if inCycle[candidate] {
				next = candidate
				break
			}
		}
		if next == "" {
			return nil
		}
		current = next
	}
	return nil
}

// Order returns the topological order, upstream first.
func (g *Graph) Order() []string {
	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// SiteIDs returns every site identifier in ascending order.
func (g *Graph) SiteIDs() []string {
	out := make([]string, len(g.ids))
	copy(out, g.ids)
	return out
}

// Site returns the named site.
func (g *Graph) Site(id string) (model.Site, bool) {
	site, ok := g.sites[id]
	return site, ok
}

// DirectUpstream returns the sites that flow straight into id.
func (g *Graph) DirectUpstream(id string) []string {
	out := make([]string, len(g.upstream[id]))
	copy(out, g.upstream[id])
	return out
}

// DirectDownstream returns the sites id flows straight into.
func (g *Graph) DirectDownstream(id string) []string {
	out := make([]string, len(g.downstream[id]))
	copy(out, g.downstream[id])
	return out
}

// Headwaters are the sites with nothing upstream of them.
func (g *Graph) Headwaters() []string {
	out := make([]string, 0, len(g.ids))
	for _, id := range g.ids {
		if len(g.upstream[id]) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// Outlets are the sites with nothing downstream of them.
func (g *Graph) Outlets() []string {
	out := make([]string, 0, len(g.ids))
	for _, id := range g.ids {
		if len(g.downstream[id]) == 0 {
			out = append(out, id)
		}
	}
	return out
}

// Ancestors returns every site transitively upstream of id, ascending.
func (g *Graph) Ancestors(id string) []string {
	return g.reach(id, g.upstream)
}

// Descendants returns every site transitively downstream of id, ascending.
func (g *Graph) Descendants(id string) []string {
	return g.reach(id, g.downstream)
}

func (g *Graph) reach(start string, edges map[string][]string) []string {
	seen := make(map[string]bool)
	stack := append([]string(nil), edges[start]...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[current] || current == start {
			continue
		}
		seen[current] = true
		stack = append(stack, edges[current]...)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// TravelHours returns the shortest nominal transit time from an upstream site to
// a downstream one, and whether a path exists at all. Shortest is used because
// when two routes exist, the quickest one bounds how soon material could
// possibly appear downstream.
func (g *Graph) TravelHours(from, to string) (float64, bool) {
	if from == to {
		return 0, true
	}
	best := make(map[string]float64, len(g.ids))
	best[from] = 0
	found := false
	total := 0.0
	for _, id := range g.order {
		cost, reached := best[id]
		if !reached {
			continue
		}
		for _, next := range g.downstream[id] {
			candidate := cost + g.travel[edgeKey(id, next)]
			if existing, seen := best[next]; !seen || candidate < existing {
				best[next] = candidate
			}
		}
	}
	if cost, reached := best[to]; reached {
		found = true
		total = cost
	}
	return numeric.Round2(total), found
}

// FlowLitresPerDay is the site's mean daily flow in litres.
func (g *Graph) FlowLitresPerDay(id string) float64 {
	site, ok := g.sites[id]
	if !ok {
		return 0
	}
	return site.FlowLitresPerDay()
}

// Describe renders the topology as one line per site, upstream first.
func (g *Graph) Describe() []string {
	lines := make([]string, 0, len(g.order))
	for _, id := range g.order {
		down := g.DirectDownstream(id)
		target := "outlet"
		if len(down) > 0 {
			target = strings.Join(down, ",")
		}
		site := g.sites[id]
		lines = append(lines, fmt.Sprintf("%-14s %-20s -> %-24s flow %-12.6g pop %d",
			id, site.Matrix, target, site.FlowLitresPerDay(), site.ServedPopulation))
	}
	return lines
}
