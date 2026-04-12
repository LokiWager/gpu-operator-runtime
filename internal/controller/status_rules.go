package controller

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type statusRule[I any, O any] struct {
	Match func(I) bool
	Build func(I) O
}

type conditionDecision struct {
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

func resolveStatusRule[I any, O any](input I, rules []statusRule[I, O], fallback func(I) O) O {
	for _, rule := range rules {
		if rule.Match(input) {
			return rule.Build(input)
		}
	}
	return fallback(input)
}

func statusConditionFromDecision(conditionType string, generation int64, decision conditionDecision) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             decision.Status,
		ObservedGeneration: generation,
		Reason:             decision.Reason,
		Message:            decision.Message,
	}
}
