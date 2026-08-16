package pipeline

import (
	"fmt"

	"FluWatershed/internal/catchment"
	"FluWatershed/internal/model"
	"FluWatershed/internal/strictjson"
)

// Sources names the input files an ingest or a one-shot analysis reads. Only the
// network path is mandatory; a run may legitimately carry samples with no
// carcass events, or a network with no observations yet.
type Sources struct {
	NetworkPath string
	SamplesPath string
	ResultsPath string
	EventsPath  string
}

// Load reads every provided source strictly and returns the assembled bundle in
// canonical order. Nothing is validated here beyond decoding; Validate is a
// separate step so that a caller can report all problems at once.
func Load(sources Sources) (model.Bundle, error) {
	if sources.NetworkPath == "" {
		return model.Bundle{}, fmt.Errorf("a catchment definition path is required")
	}
	var bundle model.Bundle
	if err := strictjson.File(sources.NetworkPath, &bundle.Network); err != nil {
		return model.Bundle{}, err
	}
	if sources.SamplesPath != "" {
		if err := strictjson.RecordsFile(sources.SamplesPath, func(line int, raw []byte) error {
			var sample model.Sample
			if err := strictjson.Document(raw, &sample); err != nil {
				return err
			}
			bundle.Samples = append(bundle.Samples, sample)
			return nil
		}); err != nil {
			return model.Bundle{}, err
		}
	}
	if sources.ResultsPath != "" {
		if err := strictjson.RecordsFile(sources.ResultsPath, func(line int, raw []byte) error {
			var result model.Result
			if err := strictjson.Document(raw, &result); err != nil {
				return err
			}
			bundle.Results = append(bundle.Results, result)
			return nil
		}); err != nil {
			return model.Bundle{}, err
		}
	}
	if sources.EventsPath != "" {
		if err := strictjson.RecordsFile(sources.EventsPath, func(line int, raw []byte) error {
			var event model.CarcassEvent
			if err := strictjson.Document(raw, &event); err != nil {
				return err
			}
			bundle.Events = append(bundle.Events, event)
			return nil
		}); err != nil {
			return model.Bundle{}, err
		}
	}
	bundle.SortAll()
	return bundle, nil
}

// ValidationReport is the outcome of validating a bundle, including the topology
// check that only the graph builder can perform.
type ValidationReport struct {
	CatchmentID  string          `json:"catchment_id"`
	Sites        int             `json:"sites"`
	Links        int             `json:"links"`
	Samples      int             `json:"samples"`
	Results      int             `json:"results"`
	Events       int             `json:"events"`
	Topology     []string        `json:"topological_order"`
	Headwaters   []string        `json:"headwaters"`
	Outlets      []string        `json:"outlets"`
	Problems     []model.Problem `json:"problems"`
	TopologyNote string          `json:"topology_note"`
	Valid        bool            `json:"valid"`
}

// Validate runs the whole validation suite over a bundle. Structural problems and
// the topology check are reported together, but the topology is only attempted
// when the structural checks left the graph worth building.
func Validate(bundle model.Bundle) ValidationReport {
	problems := model.ValidateBundle(bundle)
	problems.Sort()
	report := ValidationReport{
		CatchmentID: bundle.Network.CatchmentID,
		Sites:       len(bundle.Network.Sites),
		Links:       len(bundle.Network.Links),
		Samples:     len(bundle.Samples),
		Results:     len(bundle.Results),
		Events:      len(bundle.Events),
		Problems:    problems,
	}
	graph, err := catchment.Build(bundle.Network)
	if err != nil {
		report.TopologyNote = err.Error()
		report.Problems = append(report.Problems, model.Problem{
			Scope: "topology", Ref: bundle.Network.CatchmentID, Message: err.Error(),
		})
		report.Valid = false
		return report
	}
	report.Topology = graph.Order()
	report.Headwaters = graph.Headwaters()
	report.Outlets = graph.Outlets()
	report.TopologyNote = fmt.Sprintf("acyclic with %d site(s), %d headwater(s) and %d outlet(s)",
		len(report.Topology), len(report.Headwaters), len(report.Outlets))
	report.Valid = len(report.Problems) == 0
	return report
}

// Err collapses the report into an error when it found problems.
func (r ValidationReport) Err() error {
	if r.Valid {
		return nil
	}
	return model.Problems(r.Problems).Err()
}
