package provider

import (
	"fmt"
	"maps"
	"slices"
)

// DecisionCodec is a protocol's own wire format for typed decision questions.
// Questions and answers are keyed by Crude's local slot names; a codec never
// accepts an answer for a slot it did not ask.
type DecisionCodec interface {
	EncodeDecision(model, state string, questions map[string]DecisionQuestion) map[string]any
	DecodeDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error)
}

// Ollama's decision endpoint takes questions and returns answers as objects
// keyed by slot: "noul" is the probability that the statement is true.
func (s *OllamaStandard) EncodeDecision(model, state string, questions map[string]DecisionQuestion) map[string]any {
	return map[string]any{"model": model, "state": state, "questions": questions}
}

func (s *OllamaStandard) DecodeDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	answers, ok := reply["answers"].(map[string]any)
	if !ok || len(answers) != len(questions) {
		return nil, fmt.Errorf("incomplete decision answers")
	}
	out := make(map[string]DecisionAnswer, len(questions))
	for key, q := range questions {
		a, _ := answers[key].(map[string]any)
		if a == nil {
			return nil, fmt.Errorf("incomplete decision answers")
		}
		if a["type"] == "refusal" {
			out[key] = DecisionAnswer{Refused: true}
			continue
		}
		if a["type"] != q.Type {
			return nil, fmt.Errorf("invalid decision answer")
		}
		var answer DecisionAnswer
		var err error
		if q.Type == "noul" {
			if answer.Noul, err = probability(a["noul"]); err != nil {
				return nil, err
			}
		} else {
			answer.Choice, _ = a["choice"].(string)
			probs, _ := a["probabilities"].(map[string]any)
			if _, known := q.Criteria[answer.Choice]; !known || len(probs) != len(q.Criteria) {
				return nil, fmt.Errorf("invalid decision answer")
			}
			answer.Probabilities = make(map[string]float64, len(probs))
			for label := range q.Criteria {
				if answer.Probabilities[label], err = probability(probs[label]); err != nil {
					return nil, err
				}
			}
		}
		out[key] = answer
	}
	return out, nil
}

// The OpenAI Decisions API (POST /v1/decisions) takes shared input and an
// array of named questions: "predicate" for a probability, "choice" for one
// of the supplied values. Answers come back as an array echoing each name.
func (s *OpenAIStandard) EncodeDecision(model, state string, questions map[string]DecisionQuestion) map[string]any {
	return encodeOpenAIDecision(model, state, questions)
}

func (s *OpenAIStandard) DecodeDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	return decodeOpenAIDecision(reply, questions)
}

func (s *OpenAIResponsesStandard) EncodeDecision(model, state string, questions map[string]DecisionQuestion) map[string]any {
	return encodeOpenAIDecision(model, state, questions)
}

func (s *OpenAIResponsesStandard) DecodeDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	return decodeOpenAIDecision(reply, questions)
}

func encodeOpenAIDecision(model, state string, questions map[string]DecisionQuestion) map[string]any {
	items := make([]any, 0, len(questions))
	for _, name := range slices.Sorted(maps.Keys(questions)) {
		q := questions[name]
		item := map[string]any{"type": "predicate", "name": name, "instructions": q.Instructions}
		if q.Type == "choice" {
			choices := make([]any, 0, len(q.Criteria))
			for _, value := range slices.Sorted(maps.Keys(q.Criteria)) {
				choices = append(choices, map[string]any{"value": value, "description": q.Criteria[value]})
			}
			item["type"], item["choices"] = "choice", choices
		}
		items = append(items, item)
	}
	return map[string]any{"model": model, "input": state, "questions": items}
}

func decodeOpenAIDecision(reply map[string]any, questions map[string]DecisionQuestion) (map[string]DecisionAnswer, error) {
	answers, ok := reply["answers"].([]any)
	if !ok || len(answers) != len(questions) {
		return nil, fmt.Errorf("incomplete decision answers")
	}
	out := make(map[string]DecisionAnswer, len(questions))
	for _, raw := range answers {
		a, _ := raw.(map[string]any)
		name, _ := a["name"].(string)
		q, asked := questions[name]
		if _, seen := out[name]; !asked || seen {
			return nil, fmt.Errorf("invalid decision answer")
		}
		var answer DecisionAnswer
		var err error
		switch {
		case a["type"] == "refusal":
			answer.Refused = true
		case a["type"] == "predicate" && q.Type == "noul":
			if answer.Noul, err = probability(a["probability"]); err != nil {
				return nil, err
			}
		case a["type"] == "choice" && q.Type == "choice":
			answer.Choice, _ = a["choice"].(string)
			probs, _ := a["probabilities"].([]any)
			if _, known := q.Criteria[answer.Choice]; !known || len(probs) != len(q.Criteria) {
				return nil, fmt.Errorf("invalid decision answer")
			}
			answer.Probabilities = make(map[string]float64, len(probs))
			for _, rawProb := range probs {
				entry, _ := rawProb.(map[string]any)
				value, _ := entry["value"].(string)
				if _, known := q.Criteria[value]; !known {
					return nil, fmt.Errorf("invalid decision answer")
				}
				if _, repeated := answer.Probabilities[value]; repeated {
					return nil, fmt.Errorf("invalid decision answer")
				}
				if answer.Probabilities[value], err = probability(entry["probability"]); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("invalid decision answer")
		}
		out[name] = answer
	}
	return out, nil
}
